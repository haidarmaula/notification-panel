package daemon

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/dispatch"
	"hello/internal/fcm"
	"hello/internal/installation"
	"hello/internal/jsonobject"
	"hello/internal/pushpayload"
	"hello/internal/receipt"
)

// The protected worker policy supplies installation and receipt key rings.
// Only the FCM credential factory reads a separate managed secret.
type deliveryConfig struct {
	application pgtype.UUID
	poll        time.Duration
	pageSize    int32
	renew       time.Duration
	execution   dispatch.ExecutionPolicy
	transport   fcm.Config
	credential  fcm.FileCredentialPolicy
	signer      *receipt.Signer
	protector   *installation.Protector
}

func parseDelivery(raw json.RawMessage) (*deliveryConfig, error) {
	var d deliveryConfig
	var application, fingerprint string
	var routes, encryption, signing []json.RawMessage
	var pollMS, lease, renew, request, retry, observation, cooldown int32
	var version, projectRate, replicas int32
	p := &d.execution.Receipt
	err := jsonobject.Decode(raw, map[string]any{
		"application_id": &application, "environment": &p.Environment,
		"project_id": &d.transport.ProjectID, "routes": &routes,
		"service_account_file": &d.credential.Path, "credential_project_id": &d.credential.CredentialProjectID,
		"service_account_email": &d.credential.ServiceAccountEmail, "cross_project_approved": &d.credential.CrossProjectApproved,
		"lease_seconds": &lease, "renew_seconds": &renew, "request_timeout_seconds": &request,
		"retry_floor_seconds": &retry, "max_attempts": &d.execution.MaxAttempts,
		"receipt_key_id": &p.KeyID, "observation_window_seconds": &observation,
		"requests_per_second": &d.transport.RequestsPerSecond, "project_requests_per_second": &projectRate,
		"replica_count": &replicas, "concurrency": &d.transport.Concurrency,
		"breaker_threshold": &d.transport.BreakerThreshold, "breaker_cooldown_seconds": &cooldown,
		"poll_interval_ms": &pollMS, "page_size": &d.pageSize,
		"current_key_version": &version, "fingerprint_key": &fingerprint, "encryption_keys": &encryption,
		"signing_keys": &signing,
	})
	if err != nil || d.application.Scan(application) != nil || d.application.String() != application ||
		!d.application.Valid || d.application.Bytes == [16]byte{} {
		return nil, ErrConfiguration
	}
	if len(routes) == 0 || len(routes) > 64 {
		return nil, ErrConfiguration
	}
	d.transport.Environment = p.Environment
	for _, rawRoute := range routes {
		r := pushpayload.Route{ApplicationID: d.application, Environment: p.Environment}
		if jsonobject.Decode(rawRoute, map[string]any{
			"namespace": &r.Namespace, "key": &r.Key, "platform": &r.Platform, "address_type": &r.AddressType,
		}) != nil {
			return nil, ErrConfiguration
		}
		d.transport.Routes = append(d.transport.Routes, r)
	}
	d.transport.RequestTimeout = time.Duration(request) * time.Second
	d.transport.BreakerCooldown = time.Duration(cooldown) * time.Second
	d.execution.LeaseDuration = time.Duration(lease) * time.Second
	d.execution.RetryFloor = time.Duration(retry) * time.Second
	p.ObservationWindow = time.Duration(observation) * time.Second
	d.renew = time.Duration(renew) * time.Second
	d.poll = time.Duration(pollMS) * time.Millisecond
	if d.execution.Validate() != nil || d.transport.Validate() != nil || d.credential.ValidateIdentity(d.transport.ProjectID) != nil {
		return nil, ErrConfiguration
	}
	// Leave room for a request and a fenced DB write after a renewal tick.
	// Compare by division to avoid multiplying a caller-supplied rate.
	if renew < 1 || d.renew > d.execution.LeaseDuration/3 ||
		d.renew+d.transport.RequestTimeout+5*time.Second >= d.execution.LeaseDuration ||
		replicas < 1 || projectRate < 1 || d.transport.RequestsPerSecond > int(projectRate/replicas) ||
		pollMS < 100 || pollMS > 60000 || d.pageSize < 1 || d.pageSize > 100 ||
		!filepath.IsAbs(d.credential.Path) || filepath.Clean(d.credential.Path) != d.credential.Path || d.credential.Path == "/" {
		return nil, ErrConfiguration
	}
	d.protector, d.signer, err = deliveryKeys(*p, version, fingerprint, encryption, signing)
	if err != nil {
		return nil, ErrConfiguration
	}
	return &d, nil
}

func deliveryKeys(policy receipt.Policy, current int32, fingerprint string, encryption, signing []json.RawMessage) (*installation.Protector, *receipt.Signer, error) {
	if len(encryption) == 0 || len(encryption) > 64 || len(signing) == 0 || len(signing) > 16 {
		return nil, nil, ErrConfiguration
	}
	fingerprintKey, err := decodeKey(fingerprint, 32)
	if err != nil {
		return nil, nil, err
	}
	keys := make(map[int32][]byte, len(encryption))
	for _, raw := range encryption {
		var version int32
		var value string
		if jsonobject.Decode(raw, map[string]any{"version": &version, "key": &value}) != nil || keys[version] != nil {
			return nil, nil, ErrConfiguration
		}
		keys[version], err = decodeKey(value, 32)
		if err != nil {
			return nil, nil, err
		}
	}
	protector, err := installation.NewProtector(current, keys, fingerprintKey)
	if err != nil {
		return nil, nil, ErrConfiguration
	}
	signingKeys := make(map[string]ed25519.PrivateKey, len(signing))
	for _, raw := range signing {
		var id, value string
		if jsonobject.Decode(raw, map[string]any{"id": &id, "key": &value}) != nil || signingKeys[id] != nil {
			return nil, nil, ErrConfiguration
		}
		signingKeys[id], err = decodeKey(value, ed25519.PrivateKeySize)
		if err != nil {
			return nil, nil, err
		}
		// The receipt server receives the public half. Neither half may reuse
		// installation key material distributed for a different purpose.
		seed, public := signingKeys[id][:ed25519.SeedSize], signingKeys[id][ed25519.SeedSize:]
		if bytes.Equal(seed, fingerprintKey) || bytes.Equal(public, fingerprintKey) {
			return nil, nil, ErrConfiguration
		}
		for _, key := range keys {
			if bytes.Equal(seed, key) || bytes.Equal(public, key) {
				return nil, nil, ErrConfiguration
			}
		}
	}
	if signingKeys[policy.KeyID] == nil {
		return nil, nil, ErrConfiguration
	}
	signer, err := receipt.NewSigner(policy.Environment, signingKeys)
	if err != nil {
		return nil, nil, ErrConfiguration
	}
	return protector, signer, nil
}

func decodeKey(encoded string, size int) ([]byte, error) {
	key, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(key) != size || base64.StdEncoding.EncodeToString(key) != encoded {
		return nil, ErrConfiguration
	}
	return key, nil
}

func (d *deliveryConfig) open(ctx context.Context, db dispatch.Transactor) (*dispatch.DeliveryPoller, *fcm.Client, error) {
	execution, err := dispatch.NewExecution(db, d.execution)
	if err != nil {
		return nil, nil, ErrConfiguration
	}
	client, err := fcm.NewFile(ctx, d.transport, d.credential)
	if err != nil {
		return nil, nil, ErrConfiguration
	}
	submitter, err := dispatch.NewSubmitter(execution, client, d.signer, d.protector)
	if err != nil {
		client.Close()
		return nil, nil, ErrConfiguration
	}
	poller, err := dispatch.NewDeliveryPoller(execution, submitter, dispatch.DeliveryPollPolicy{
		ApplicationID: d.application, CampaignPageSize: d.pageSize, DevicePageSize: d.pageSize,
		Concurrency: d.transport.Concurrency, RenewInterval: d.renew,
	})
	if err != nil {
		client.Close()
		return nil, nil, ErrConfiguration
	}
	return poller, client, nil
}
