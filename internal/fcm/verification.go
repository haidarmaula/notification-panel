package fcm

import (
	"context"
	"errors"
	"net/http"
	"time"

	"hello/internal/deployment"
)

// ErrVerificationTemporary reports a timeout or temporary OAuth transport
// failure without exposing credentials, assertions, tokens or provider text.
var ErrVerificationTemporary = errors.New("credential verification temporarily unavailable")

// VerifyFileCredentials performs one OAuth token exchange for a protected
// service-account file. It does not send an FCM message or return the token.
func VerifyFileCredentials(ctx context.Context, policy FileCredentialPolicy, targetProject string) error {
	if ctx == nil || policy.ValidateIdentity(targetProject) != nil {
		return ErrConfiguration
	}
	if ctx.Err() != nil {
		return ErrVerificationTemporary
	}
	data, err := deployment.ReadSecret(policy.Path)
	if err != nil {
		if ctx.Err() != nil {
			return ErrVerificationTemporary
		}
		return ErrConfiguration
	}
	return verifyFileCredentials(ctx, policy, targetProject, data, 10*time.Second, nil)
}

// verifyFileCredentials isolates the OAuth exchange for deterministic transport
// tests. Production callers always use verifiedHTTP and a fixed ten-second limit.
func verifyFileCredentials(ctx context.Context, policy FileCredentialPolicy, targetProject string, data []byte, timeout time.Duration, testTransport http.RoundTripper) error {
	if ctx == nil || policy.ValidateIdentity(targetProject) != nil {
		return ErrConfiguration
	}
	if ctx.Err() != nil {
		return ErrVerificationTemporary
	}
	source, closeSource, err := accountSource(targetProject, policy, data, timeout, testTransport)
	if err != nil {
		return ErrConfiguration
	}
	defer func() {
		source.cached = nil
		source.provider = nil
		closeSource()
	}()
	token, err := source.Token(ctx)
	if token != nil {
		token.Value = ""
		token.Expiry = time.Time{}
	}
	if source.cached != nil {
		source.cached = nil
	}
	if err == nil {
		return nil
	}
	if errors.Is(err, errTokenTemporary) || ctx.Err() != nil {
		return ErrVerificationTemporary
	}
	return ErrCredentials
}
