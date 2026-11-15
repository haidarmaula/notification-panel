package onboarding

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const validVerifierPolicy = `{"application_id":"11111111-1111-4111-8111-111111111111","environment":"staging","credential_root":"/etc/hello/credentials","database_url":"postgres://verifier:secret-marker@db.example.test:5432/hello?sslmode=verify-full"}`

func TestParseVerifierPolicyUsesDefaultAndConfiguredPolling(t *testing.T) {
	policy, err := ParseVerifierPolicy([]byte(validVerifierPolicy))
	if err != nil {
		t.Fatalf("ParseVerifierPolicy(): %v", err)
	}
	if policy.Environment != EnvironmentStaging || policy.CredentialRoot != "/etc/hello/credentials" || policy.PollInterval != time.Second {
		t.Fatalf("unexpected policy: %#v", policy)
	}
	configured := strings.TrimSuffix(validVerifierPolicy, "}") + `,"poll_interval_ms":30000}`
	policy, err = ParseVerifierPolicy([]byte(configured))
	if err != nil || policy.PollInterval != 30*time.Second {
		t.Fatalf("configured poll policy = %#v, %v", policy, err)
	}
}

func TestParseVerifierPolicyRejectsMalformedPolicy(t *testing.T) {
	unknown := strings.TrimSuffix(validVerifierPolicy, "}") + `,"debug_secret":"secret-marker"}`
	duplicate := strings.TrimSuffix(validVerifierPolicy, "}") + `,"environment":"production"}`
	alias := strings.Replace(validVerifierPolicy, `"application_id"`, `"Application_id"`, 1)
	missing := strings.Replace(validVerifierPolicy, `,"database_url":"postgres://verifier:secret-marker@db.example.test:5432/hello?sslmode=verify-full"`, "", 1)
	for name, data := range map[string][]byte{
		"unknown field":          []byte(unknown),
		"duplicate field":        []byte(duplicate),
		"case alias":             []byte(alias),
		"missing required field": []byte(missing),
		"null optional field":    []byte(strings.TrimSuffix(validVerifierPolicy, "}") + `,"poll_interval_ms":null}`),
		"bad poll number":        []byte(strings.TrimSuffix(validVerifierPolicy, "}") + `,"poll_interval_ms":1.5}`),
		"invalid UTF-8":          append([]byte(`{"`), 0xff),
		"oversized":              append([]byte(validVerifierPolicy), make([]byte, verifierPolicyLimit+1)...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseVerifierPolicy(data); !errors.Is(err, ErrInvalidVerifierPolicy) || strings.Contains(err.Error(), "secret-marker") {
				t.Fatalf("ParseVerifierPolicy() error = %v", err)
			}
		})
	}
}

func TestParseVerifierPolicyRejectsOutOfRangeAndUnsafeValues(t *testing.T) {
	for name, replacement := range map[string]string{
		"too frequent":             `,"poll_interval_ms":999`,
		"too slow":                 `,"poll_interval_ms":30001`,
		"invalid app UUID":         `"application_id":"00000000-0000-0000-0000-000000000000"`,
		"invalid environment":      `"environment":"development"`,
		"relative credential root": `"credential_root":"credentials"`,
		"unclean credential root":  `"credential_root":"/etc/hello/../credentials"`,
		"empty database URL":       `"database_url":" "`,
	} {
		t.Run(name, func(t *testing.T) {
			body := validVerifierPolicy
			if strings.HasPrefix(replacement, `,"poll_interval_ms"`) {
				body = strings.TrimSuffix(body, "}") + replacement + `}`
			} else {
				switch name {
				case "invalid app UUID":
					body = strings.Replace(body, `"application_id":"11111111-1111-4111-8111-111111111111"`, replacement, 1)
				case "invalid environment":
					body = strings.Replace(body, `"environment":"staging"`, replacement, 1)
				case "relative credential root", "unclean credential root":
					body = strings.Replace(body, `"credential_root":"/etc/hello/credentials"`, replacement, 1)
				case "empty database URL":
					body = strings.Replace(body, `"database_url":"postgres://verifier:secret-marker@db.example.test:5432/hello?sslmode=verify-full"`, replacement, 1)
				}
			}
			if _, err := ParseVerifierPolicy([]byte(body)); !errors.Is(err, ErrInvalidVerifierPolicy) {
				t.Fatalf("ParseVerifierPolicy() error = %v", err)
			}
		})
	}
}
