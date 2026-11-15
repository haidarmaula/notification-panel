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
)

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
