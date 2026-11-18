package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"hello/internal/deployment"
	"hello/internal/observability"
)

const (
	// DeliveryPreparationCredentialPath is the fixed worker-side credential path.
	DeliveryPreparationCredentialPath = "/etc/hello/fcm/service-account.json"
	// DeliveryPreparationDBCAPath is the only optional worker-side DB TLS file.
	DeliveryPreparationDBCAPath = "/etc/hello/postgres-ca.crt"
)

// DeliveryPreparationScope is the application and capacity authorized by the host plan.
type DeliveryPreparationScope struct {
	ApplicationID       string
	Environment         string
	DatabaseConnections int32
}

// DeliveryPreparationMetadata contains only identifiers needed by the host to
// check protected approval and select the fixed optional CA mount.
type DeliveryPreparationMetadata struct {
	ProjectID           string
	CredentialProjectID string
	ServiceAccountEmail string
	DatabaseUsername    string
	NeedsDBCA           bool
}

// ValidateDeliveryPreparationPolicy validates an operator-protected delivery
// policy without opening database TLS files, credentials or network connections.
// The worker repeats validation and checks those resources inside its container.
func ValidateDeliveryPreparationPolicy(data []byte, scope DeliveryPreparationScope) (DeliveryPreparationMetadata, error) {
	var metadata DeliveryPreparationMetadata
	cfg, needsDBCA, err := loadDeliveryPreparationPolicy(data, false)
	if err != nil {
		return metadata, ErrConfiguration
	}
	d := cfg.delivery
	if d.application.String() != scope.ApplicationID || d.transport.Environment != scope.Environment ||
		d.databaseMaxConns != scope.DatabaseConnections {
		return metadata, ErrConfiguration
	}
	return DeliveryPreparationMetadata{
		ProjectID: d.transport.ProjectID, CredentialProjectID: d.credential.CredentialProjectID,
		ServiceAccountEmail: d.credential.ServiceAccountEmail, DatabaseUsername: cfg.database.ConnConfig.User,
		NeedsDBCA: needsDBCA,
	}, nil
}

func loadDeliveryPreparationPolicy(data []byte, loadDBCA bool) (*Configuration, bool, error) {
	var needsDBCA bool
	cfg, err := loadPolicy(deployment.Worker, bytes.NewReader(data), func(raw, applicationName string) (*pgxpool.Config, error) {
		connection, err := url.Parse(raw)
		if err != nil {
			return nil, ErrConfiguration
		}
		query, err := url.ParseQuery(connection.RawQuery)
		if err != nil {
			return nil, ErrConfiguration
		}
		ca := query["sslrootcert"]
		if len(ca) > 1 || query.Get("sslcert") != "" || query.Get("sslkey") != "" ||
			(len(ca) == 1 && ca[0] != "" && ca[0] != DeliveryPreparationDBCAPath) {
			return nil, ErrConfiguration
		}
		needsDBCA = len(ca) == 1 && ca[0] == DeliveryPreparationDBCAPath
		if needsDBCA && !loadDBCA {
			// Preserve all other DSN fields for the normal validator. The
			// container sees the original DSN and validates the mounted CA.
			query.Del("sslrootcert")
			connection.RawQuery = query.Encode()
			raw = connection.String()
		}
		if loadDBCA && deployment.CheckDatabaseFiles(raw) != nil {
			return nil, ErrConfiguration
		}
		return deployment.Database(raw, applicationName)
	})
	if err != nil || cfg.delivery == nil || cfg.monitoring != nil ||
		!cfg.delivery.acceptsPreparation() {
		return nil, false, ErrConfiguration
	}
	return cfg, needsDBCA, nil
}

func (d *deliveryConfig) acceptsPreparation() bool {
	return (d.transport.Environment == "staging" || d.transport.Environment == "production") &&
		d.credential.Path == DeliveryPreparationCredentialPath &&
		!d.credential.CrossProjectApproved && d.credential.CredentialProjectID == d.transport.ProjectID &&
		d.replicas == 1 && d.transport.Concurrency == 1 && d.transport.RequestsPerSecond == 1 && d.projectRate == 1 &&
		d.databaseMaxConns >= 2 && d.databaseMaxConns <= 10
}

// DeliveryPreflight reports the application's scope and digest of the protected
// policy loaded by the one-shot worker. The digest is for the trusted host only
// and describes the input file, never a credential file or an active process.
type DeliveryPreflight struct {
	ApplicationID string `json:"application_id"`
	PolicySHA256  string `json:"policy_sha256"`
}

// CheckDeliveryDeployment loads the fixed protected policy and runs the delivery
// worker's database, Kafka metadata and local credential startup checks. It
// never starts a daemon, produces to Kafka, claims work or obtains an OAuth token.
func CheckDeliveryDeployment(ctx context.Context) (DeliveryPreflight, error) {
	var result DeliveryPreflight
	data, err := deployment.Read(deployment.Worker)
	if err != nil {
		return result, ErrConfiguration
	}
	cfg, _, err := loadDeliveryPreparationPolicy(data, true)
	if err != nil {
		return result, ErrConfiguration
	}
	if err := cfg.checkDeliveryStartup(ctx); err != nil {
		return result, err
	}
	digest := sha256.Sum256(data)
	return DeliveryPreflight{
		ApplicationID: cfg.delivery.application.String(),
		PolicySHA256:  hex.EncodeToString(digest[:]),
	}, nil
}

func (c *Configuration) checkDeliveryStartup(ctx context.Context) (err error) {
	startup, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	pool, producer, err := c.openStartup(startup)
	if err != nil {
		return err
	}
	defer func() {
		if producer.Close() != nil {
			err = errors.Join(err, errDrain)
		}
		pool.Close()
		if err == nil {
			err = startup.Err()
		}
	}()
	var allowed bool
	// The existing delivery role supplies column privileges on applications.
	// Verify a restricted LOGIN and the configured app's existence, without
	// suggesting the shared database role limits its member to that app.
	err = pool.QueryRow(startup, `SELECT current_user = session_user AND session_user::text COLLATE "C" = $2 COLLATE "C"
		AND EXISTS (SELECT FROM pg_catalog.pg_roles login CROSS JOIN pg_catalog.pg_roles delivery
			WHERE login.rolname = session_user AND login.rolcanlogin AND NOT login.rolsuper
			AND NOT login.rolcreatedb AND NOT login.rolcreaterole AND NOT login.rolreplication AND NOT login.rolbypassrls
			AND delivery.rolname = 'hello_delivery' AND NOT delivery.rolcanlogin AND NOT delivery.rolsuper
			AND NOT delivery.rolcreatedb AND NOT delivery.rolcreaterole AND NOT delivery.rolreplication AND NOT delivery.rolbypassrls
			AND pg_catalog.pg_has_role(login.oid, delivery.oid, 'USAGE')
			AND NOT EXISTS (SELECT FROM pg_catalog.pg_auth_members membership
				WHERE membership.member = delivery.oid OR (membership.member = login.oid AND membership.roleid <> delivery.oid))
			AND NOT EXISTS (SELECT FROM pg_catalog.pg_shdepend dependency
				WHERE dependency.refclassid = 'pg_catalog.pg_authid'::pg_catalog.regclass
				AND dependency.refobjid IN (login.oid, delivery.oid) AND dependency.deptype = 'o'))
		AND EXISTS (SELECT FROM public.applications WHERE public_id = $1 AND status IN ('PROVISIONING','ACTIVE'))`,
		c.delivery.application, c.database.ConnConfig.User).Scan(&allowed)
	if err != nil || !allowed {
		return errDatabase
	}
	state := observability.New("delivery_recovery", "delivery_updates")
	_, client, err := c.delivery.open(startup, pool, state.Provider())
	if err != nil {
		return ErrConfiguration
	}
	client.Close()
	// Validate the last constructor of the delivery path without publishing.
	_, err = c.deliveryOutbox(pool, producer)
	if err != nil {
		return ErrConfiguration
	}
	return nil
}
