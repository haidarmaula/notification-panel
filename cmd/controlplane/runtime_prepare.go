package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/daemon"
	"hello/internal/database"
	"hello/internal/deployment"
	"hello/internal/jsonobject"
	"hello/internal/onboarding"
	"hello/internal/provisioning"
	"hello/internal/runloop"
)

const (
	runtimePreparationSettingsPath = "/etc/hello/runtime-preparation.json"
	runtimePreparationPolicyPath   = "/etc/hello/runtime-preparation-policy.json"
	runtimePreparationDeliveryPath = "/etc/hello/runtime-preparation-worker.json"
)

type runtimePreparationSettings struct {
	databaseURL     string
	applicationID   string
	configurationID string
	environment     onboarding.Environment
	generation      int64
	reservation     onboarding.RuntimeReservationRequest
}

func parseRuntimePreparationSettings(data []byte) (runtimePreparationSettings, error) {
	var settings runtimePreparationSettings
	if len(data) == 0 || len(data) > 64*1024 || !utf8.Valid(data) || !jsonobject.UniqueLowercaseFields(data) {
		return settings, onboarding.ErrRuntimeReservationUnavailable
	}
	err := jsonobject.Decode(data, map[string]any{
		"database_url": &settings.databaseURL, "application_id": &settings.applicationID,
		"configuration_id": &settings.configurationID, "environment": &settings.environment,
		"generation": &settings.generation, "reservation_id": &settings.reservation.ID,
		"setup_revision": &settings.reservation.SetupRevision,
		"origin_epoch":   &settings.reservation.OriginEpoch, "source_epoch": &settings.reservation.SourceEpoch,
	})
	if err != nil || settings.databaseURL == "" || settings.generation <= 0 || settings.reservation.SetupRevision <= 0 ||
		(settings.environment != onboarding.EnvironmentStaging && settings.environment != onboarding.EnvironmentProduction) {
		return runtimePreparationSettings{}, onboarding.ErrRuntimeReservationUnavailable
	}
	for _, value := range []string{settings.applicationID, settings.configurationID, settings.reservation.ID,
		settings.reservation.OriginEpoch, settings.reservation.SourceEpoch} {
		var id pgtype.UUID
		if id.Scan(value) != nil || !id.Valid || id.String() != value || value == "00000000-0000-0000-0000-000000000000" {
			return runtimePreparationSettings{}, onboarding.ErrRuntimeReservationUnavailable
		}
	}
	return settings, nil
}

// runRuntimePreparation is a separate operator-only, one-shot preparation mode.
// It never uses the OAuth identity or exposes a privileged command through HTTP.
func runRuntimePreparation(mode string) int {
	preflight := mode != "prepare-runtime"
	restore, err := runloop.PrepareCommandOutputs()
	if err != nil {
		return 1
	}
	defer restore()
	settingsData, err := deployment.ReadSecret(runtimePreparationSettingsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid runtime preparation configuration")
		return 1
	}
	settings, err := parseRuntimePreparationSettings(settingsData)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid runtime preparation configuration")
		return 1
	}
	policyData, err := deployment.ReadSecret(runtimePreparationPolicyPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid runtime preparation policy")
		return 1
	}
	policy, err := onboarding.ParseRuntimePolicy(policyData)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid runtime preparation policy")
		return 1
	}
	plan, err := policy.BuildRuntimePlan(settings.applicationID, settings.configurationID, settings.environment, settings.generation)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid runtime preparation scope")
		return 1
	}
	var workerPolicy []byte
	var evidence onboarding.DeliveryPreflightEvidence
	if preflight {
		workerPolicy, evidence, err = readDeliveryPreparationPolicy(plan.Delivery())
		if err != nil {
			fmt.Fprintln(os.Stderr, "invalid protected delivery worker policy")
			return 1
		}
	}
	settings.reservation.PolicySHA256 = sha256.Sum256(policyData)
	dbConfig, err := deployment.Database(settings.databaseURL, "hello-runtime-preparation")
	if err != nil || deployment.CheckDatabaseFiles(settings.databaseURL) != nil {
		fmt.Fprintln(os.Stderr, "invalid runtime preparation database configuration")
		return 1
	}
	dbConfig.MaxConns, dbConfig.MinConns = 1, 0
	dbConfig.ConnConfig.RuntimeParams["search_path"] = "pg_catalog,pg_temp"
	dbConfig.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	dbConfig.ConnConfig.RuntimeParams["lock_timeout"] = "2000"
	dbConfig.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "5000"
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	defer runloop.InterruptOutputOnCancel(ctx, os.Stdout, os.Stderr)()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, err := database.NewWithConfig(ctx, dbConfig)
	if err != nil {
		fmt.Fprintln(os.Stderr, "runtime preparation database unavailable")
		return 1
	}
	defer pool.Close()
	reservation, err := onboarding.ReserveRuntime(ctx, pool, plan, settings.reservation)
	if err != nil {
		fmt.Fprintln(os.Stderr, "runtime preparation reservation unavailable")
		return 1
	}
	preparer := onboarding.NewRuntimePreparer(reservation)
	defer preparer.Close()
	if preflight {
		if err := reservation.CheckDeliveryPreflight(ctx, plan.Delivery(), evidence); err != nil {
			fmt.Fprintln(os.Stderr, "delivery preflight authority unavailable")
			return 1
		}
		if _, err := provisioning.PublishDeliveryPolicy(ctx, settings.applicationID, string(settings.environment),
			settings.configurationID, settings.generation, workerPolicy); err != nil {
			fmt.Fprintln(os.Stderr, "delivery policy publication unavailable")
			return 1
		}
		if mode == "start-delivery" {
			if _, err := preparer.StartDelivery(ctx, plan, evidence); err != nil {
				fmt.Fprintln(os.Stderr, "delivery startup unavailable; retain reservation for reconciliation")
				return 1
			}
			fmt.Fprintln(os.Stdout, "Delivery container running; live readiness must be checked separately")
			return 0
		}
		if _, err := preparer.RunDeliveryPreflight(ctx, plan, evidence); err != nil {
			fmt.Fprintln(os.Stderr, "delivery preflight unavailable; retain reservation for reconciliation")
			return 1
		}
		fmt.Fprintln(os.Stdout, "Delivery worker policy preflight passed")
		return 0
	}
	if _, err := preparer.Prepare(ctx, plan); err != nil {
		fmt.Fprintln(os.Stderr, "runtime preparation unavailable; retain reservation for reconciliation")
		return 1
	}
	fmt.Fprintln(os.Stdout, "Stopped runtime prepared; enrollment and dispatch remain unchanged")
	return 0
}

func readDeliveryPreparationPolicy(plan onboarding.RuntimeDeliveryPlan) ([]byte, onboarding.DeliveryPreflightEvidence, error) {
	policy, err := deployment.ReadSecret(runtimePreparationDeliveryPath)
	if err != nil {
		return nil, onboarding.DeliveryPreflightEvidence{}, err
	}
	metadata, err := daemon.ValidateDeliveryPreparationPolicy(policy, daemon.DeliveryPreparationScope{
		ApplicationID: plan.ApplicationID, Environment: string(plan.Environment),
		DatabaseConnections: plan.DatabaseConnections,
	})
	if err != nil {
		return nil, onboarding.DeliveryPreflightEvidence{}, err
	}
	return policy, onboarding.DeliveryPreflightEvidence{
		ProjectID: metadata.ProjectID, ServiceAccountEmail: metadata.ServiceAccountEmail,
		DatabaseUsername: metadata.DatabaseUsername, PolicySHA256: sha256.Sum256(policy), NeedsDBCA: metadata.NeedsDBCA,
	}, nil
}
