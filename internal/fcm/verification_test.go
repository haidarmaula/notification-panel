package fcm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hello/internal/installation"
)

func TestValidateFileTarget(t *testing.T) {
	data, policy, key := fileFixture(t)
	for _, tt := range []struct {
		name, kind, field, body string
		status                  int
		want                    error
	}{
		{"fid", installation.FCMFID, "fid", `{}`, 200, nil},
		{"token", installation.FCMRegistrationToken, "token", `{"name":"ignored"}`, 200, nil},
		{"unregistered", installation.FCMFID, "fid", `{"error":{"details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`, 404, ErrTargetUnregistered},
		{"mismatch", installation.FCMFID, "fid", `{"error":{"details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"SENDER_ID_MISMATCH"}]}}`, 403, ErrTargetSenderMismatch},
		{"ambiguous", installation.FCMFID, "fid", `{"error":{"message":"UNREGISTERED synthetic-target"}}`, 404, ErrTargetCheckRejected},
		{"permission", installation.FCMFID, "fid", `{}`, 403, ErrCredentials},
		{"rate limit", installation.FCMFID, "fid", `{}`, 429, ErrVerificationTemporary},
		{"redirect", installation.FCMFID, "fid", `{}`, 307, ErrVerificationTemporary},
		{"bad response", installation.FCMFID, "fid", `null`, 200, ErrVerificationTemporary},
		{"oversize", installation.FCMFID, "fid", strings.Repeat("x", 64*1024+1), 200, ErrVerificationTemporary},
		{"network", installation.FCMFID, "fid", "", 0, ErrVerificationTemporary},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					assertOAuthAssertion(t, r, &key.PublicKey)
					return response(http.StatusOK, `{"access_token":"synthetic_access_token","token_type":"Bearer","expires_in":3600}`), nil
				}
				if calls != 2 || r.Method != http.MethodPost || r.URL.String() != "https://fcm.googleapis.com/v1/projects/fixture-project/messages:send" || r.GetBody != nil || r.Header.Get("Authorization") != "Bearer synthetic_access_token" {
					t.Fatal("unexpected dry-run transport request")
				}
				defer r.Body.Close()
				var payload struct {
					ValidateOnly bool              `json:"validate_only"`
					Message      map[string]string `json:"message"`
				}
				if json.NewDecoder(r.Body).Decode(&payload) != nil || !payload.ValidateOnly || len(payload.Message) != 1 || payload.Message[tt.field] != "synthetic-target" {
					t.Fatal("request was not a single-target dry run")
				}
				if tt.status == 0 {
					return nil, errors.New("synthetic private transport detail")
				}
				resp := response(tt.status, tt.body)
				resp.Header.Set("Location", "https://example.invalid/")
				return resp, nil
			})
			err := validateFileTarget(context.Background(), policy, "fixture-project", tt.kind, "synthetic-target", data, time.Second, transport)
			if err != tt.want || calls != 2 {
				t.Fatalf("unexpected result: error=%v, calls=%d", err, calls)
			}
		})
	}
}

func TestValidateFileTargetFailsClosedBeforeFCM(t *testing.T) {
	data, policy, _ := fileFixture(t)
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != oauthEndpoint {
			t.Fatal("failed credential contacted FCM")
		}
		return response(http.StatusBadRequest, `{"error":"invalid_grant"}`), nil
	})
	ctx := context.Background()
	if err := validateFileTarget(ctx, policy, "fixture-project", installation.FCMFID, "synthetic-target", data, time.Second, transport); err != ErrCredentials {
		t.Fatal("OAuth rejection was not classified as a credential failure")
	}
	noNetwork := roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid or cancelled check contacted a provider")
		return nil, nil
	})
	if err := validateFileTarget(ctx, policy, "fixture-project", "topic", "synthetic-target", data, time.Second, noNetwork); err != ErrConfiguration {
		t.Fatal("unsupported target accepted")
	}
	if err := validateFileTarget(ctx, policy, "fixture-project", installation.FCMFID, "", data, time.Second, noNetwork); err != ErrConfiguration {
		t.Fatal("empty target accepted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := validateFileTarget(cancelled, policy, "fixture-project", installation.FCMFID, "synthetic-target", data, time.Second, noNetwork); err != ErrVerificationTemporary {
		t.Fatal("cancelled check did not return temporary failure")
	}
}

func TestVerifyFileCredentialsExchangesOAuthWithoutSendingFCM(t *testing.T) {
	data, policy, key := fileFixture(t)
	var calls atomic.Int32
	err := verifyFileCredentials(context.Background(), policy, "fixture-project", data, 10*time.Second, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		assertOAuthAssertion(t, r, &key.PublicKey)
		if r.URL.String() != oauthEndpoint {
			t.Fatal("verification attempted a non-OAuth request")
		}
		return response(http.StatusOK, `{"access_token":"synthetic_access_token","token_type":"Bearer","expires_in":3600}`), nil
	}))
	if err != nil {
		t.Fatal("valid credential could not obtain an OAuth token")
	}
	if calls.Load() != 1 {
		t.Fatal("verification performed more than one OAuth exchange")
	}

	var fcmCalls atomic.Int32
	guard := oauthTransport{base: roundTripFunc(func(*http.Request) (*http.Response, error) {
		fcmCalls.Add(1)
		return response(http.StatusOK, `{}`), nil
	})}
	request, _ := http.NewRequest(http.MethodPost, "https://fcm.googleapis.com/v1/projects/fixture-project/messages:send", nil)
	if _, err := guard.RoundTrip(request); !errors.Is(err, ErrCredentials) || fcmCalls.Load() != 0 {
		t.Fatal("OAuth source allowed an FCM request")
	}
}

func TestVerifyFileCredentialsRejectsLocalKeyAndInvalidGrant(t *testing.T) {
	data, policy, _ := fileFixture(t)
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal("cannot decode synthetic credential")
	}
	fields["private_key"] = "not a PEM key"
	invalid, err := json.Marshal(fields)
	if err != nil {
		t.Fatal("cannot encode invalid synthetic credential")
	}
	var calls atomic.Int32
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return response(http.StatusOK, `{"access_token":"synthetic_access_token","token_type":"Bearer","expires_in":3600}`), nil
	})
	if err := verifyFileCredentials(context.Background(), policy, "fixture-project", invalid, time.Second, transport); !errors.Is(err, ErrConfiguration) {
		t.Fatal("invalid local key did not return configuration error")
	}
	if calls.Load() != 0 {
		t.Fatal("invalid local key contacted OAuth")
	}
	if err := verifyFileCredentials(context.Background(), policy, "fixture-project", data, time.Second, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusBadRequest, `{"error":"invalid_grant","error_description":"synthetic private detail"}`), nil
	})); !errors.Is(err, ErrCredentials) || err.Error() != ErrCredentials.Error() || strings.Contains(err.Error(), "private detail") {
		t.Fatal("invalid_grant did not return a sanitized permanent credential error")
	}
}

func TestVerifyFileCredentialsClassifiesTimeoutWithoutProviderDetails(t *testing.T) {
	data, policy, _ := fileFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := verifyFileCredentials(ctx, policy, "fixture-project", data, 10*time.Second, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, errors.New("synthetic sensitive network error")
	}))
	if !errors.Is(err, ErrVerificationTemporary) || err.Error() != ErrVerificationTemporary.Error() || strings.Contains(err.Error(), "sensitive") {
		t.Fatal("OAuth timeout did not return the sanitized temporary error")
	}
}

func TestVerifyFileCredentialsValidatesPolicyBeforeReadingFile(t *testing.T) {
	_, policy, _ := fileFixture(t)
	policy.Path = t.TempDir() + "/missing.json"
	if err := VerifyFileCredentials(context.Background(), policy, "different-project"); !errors.Is(err, ErrConfiguration) {
		t.Fatal("invalid identity policy was not rejected before file access")
	}
	if err := VerifyFileCredentials(context.Background(), policy, "fixture-project"); !errors.Is(err, ErrConfiguration) {
		t.Fatal("missing protected credential file was accepted")
	}
}
