package onboarding

import (
	"errors"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/jsonobject"
)

const verifierPolicyLimit = 64 * 1024

// VerifierPolicy is the protected startup configuration for one OAuth verifier process.
type VerifierPolicy struct {
	ApplicationID  pgtype.UUID
	Environment    Environment
	CredentialRoot string
	DatabaseURL    string
	PollInterval   time.Duration
}

// ErrInvalidVerifierPolicy reports malformed protected verifier configuration.
var ErrInvalidVerifierPolicy = errors.New("invalid onboarding verifier policy")

// ParseVerifierPolicy parses a bounded policy and applies the default one-second poll interval.
func ParseVerifierPolicy(data []byte) (VerifierPolicy, error) {
	var policy VerifierPolicy
	var applicationID, environment string
	pollMilliseconds := int64(1000)
	if len(data) == 0 || len(data) > verifierPolicyLimit || !utf8.Valid(data) || !jsonobject.UniqueLowercaseFields(data) {
		return policy, ErrInvalidVerifierPolicy
	}
	if jsonobject.DecodeOptional(data,
		map[string]any{
			"application_id":  &applicationID,
			"environment":     &environment,
			"credential_root": &policy.CredentialRoot,
			"database_url":    &policy.DatabaseURL,
		},
		map[string]any{"poll_interval_ms": &pollMilliseconds},
	) != nil {
		return VerifierPolicy{}, ErrInvalidVerifierPolicy
	}
	if !parseVerifierUUID(applicationID, &policy.ApplicationID) ||
		(environment != string(EnvironmentStaging) && environment != string(EnvironmentProduction)) ||
		!filepath.IsAbs(policy.CredentialRoot) || filepath.Clean(policy.CredentialRoot) != policy.CredentialRoot ||
		policy.CredentialRoot == string(filepath.Separator) || strings.TrimSpace(policy.DatabaseURL) == "" ||
		pollMilliseconds < 1000 || pollMilliseconds > 30000 {
		return VerifierPolicy{}, ErrInvalidVerifierPolicy
	}
	policy.Environment = Environment(environment)
	policy.PollInterval = time.Duration(pollMilliseconds) * time.Millisecond
	return policy, nil
}

func parseVerifierUUID(value string, id *pgtype.UUID) bool {
	return len(value) == 36 && id.Scan(value) == nil && id.Valid && id.Bytes != [16]byte{} && id.String() == value
}
