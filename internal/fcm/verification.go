package fcm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"firebase.google.com/go/v4/messaging"
	"hello/internal/deployment"
	"hello/internal/installation"
)

// ErrVerificationTemporary reports a timeout or temporary OAuth transport
// failure without exposing credentials, assertions, tokens or provider text.
var ErrVerificationTemporary = errors.New("credential verification temporarily unavailable")

// ErrTargetUnregistered reports an explicit FCM UNREGISTERED response.
var ErrTargetUnregistered = errors.New("FCM target unregistered")

// ErrTargetSenderMismatch reports an explicit FCM SENDER_ID_MISMATCH response.
var ErrTargetSenderMismatch = errors.New("FCM target sender mismatch")

// ErrTargetCheckRejected reports a permanent rejection without evidence that
// the target is unregistered or belongs to another sender.
var ErrTargetCheckRejected = errors.New("FCM target check rejected")

const verificationTimeout = 10 * time.Second

// ValidateFileTarget checks one protected installation target with FCM validate_only.
// Success means FCM accepted the dry run, not that a device is reachable.
func ValidateFileTarget(ctx context.Context, policy FileCredentialPolicy, project, addressType, address string) error {
	data, err := readVerificationCredential(ctx, policy, project)
	if err != nil {
		return err
	}
	return validateFileTarget(ctx, policy, project, addressType, address, data, verificationTimeout, nil)
}

func validateFileTarget(ctx context.Context, policy FileCredentialPolicy, project, addressType, address string, data []byte, timeout time.Duration, transport http.RoundTripper) error {
	if ctx == nil || policy.ValidateIdentity(project) != nil || !installation.ValidAddress(address) || !opaqueToken(address) {
		return ErrConfiguration
	}
	var message messaging.Message
	switch addressType {
	case installation.FCMFID:
		message.Fid = address
	case installation.FCMRegistrationToken:
		message.Token = address
	default:
		return ErrConfiguration
	}
	payload, err := json.Marshal(struct {
		ValidateOnly bool              `json:"validate_only"`
		Message      messaging.Message `json:"message"`
	}{true, message})
	if err != nil {
		return ErrConfiguration
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	base := transport
	if base == nil {
		httpTransport, err := verifiedHTTP(Config{RequestTimeout: timeout, Concurrency: 1})
		if err != nil {
			return ErrConfiguration
		}
		defer httpTransport.CloseIdleConnections()
		base = httpTransport
	}
	return withVerificationToken(ctx, policy, project, data, timeout, transport, func(token string) error {
		if ctx.Err() != nil {
			return ErrVerificationTemporary
		}
		endpoint := "https://fcm.googleapis.com/v1/projects/" + project + "/messages:send"
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return ErrConfiguration
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		// A direct RoundTrip cannot follow redirects. Disable POST replay as well.
		request.GetBody = nil
		resp, err := base.RoundTrip(request)
		if resp != nil && resp.Body != nil {
			defer resp.Body.Close()
		}
		if err != nil || resp == nil || ctx.Err() != nil {
			return ErrVerificationTemporary
		}
		return classifyTargetCheck(resp)
	})
}

func classifyTargetCheck(resp *http.Response) error {
	status := resp.StatusCode
	if status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500 {
		return ErrVerificationTemporary
	}
	var body struct {
		Error *struct {
			Details []struct {
				Type string `json:"@type"`
				Code string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	const limit = 64 * 1024
	if resp.Body == nil {
		return ErrVerificationTemporary
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || len(data) > limit {
		return ErrVerificationTemporary
	}
	valid := json.Unmarshal(data, &body) == nil && len(bytes.TrimSpace(data)) > 0 && bytes.TrimSpace(data)[0] == '{'
	if status == http.StatusOK && valid && body.Error == nil {
		return nil
	}
	if valid && body.Error != nil {
		for _, detail := range body.Error.Details {
			if detail.Type != "type.googleapis.com/google.firebase.fcm.v1.FcmError" {
				continue
			}
			switch {
			case status == http.StatusNotFound && detail.Code == "UNREGISTERED":
				return ErrTargetUnregistered
			case status == http.StatusForbidden && detail.Code == "SENDER_ID_MISMATCH":
				return ErrTargetSenderMismatch
			}
		}
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrCredentials
	case http.StatusBadRequest, http.StatusNotFound:
		return ErrTargetCheckRejected
	default:
		return ErrVerificationTemporary
	}
}

// VerifyFileCredentials performs one OAuth token exchange for a protected
// service-account file. It does not send an FCM message or return the token.
func VerifyFileCredentials(ctx context.Context, policy FileCredentialPolicy, targetProject string) error {
	data, err := readVerificationCredential(ctx, policy, targetProject)
	if err != nil {
		return err
	}
	return verifyFileCredentials(ctx, policy, targetProject, data, verificationTimeout, nil)
}

func readVerificationCredential(ctx context.Context, policy FileCredentialPolicy, targetProject string) ([]byte, error) {
	if ctx == nil || policy.ValidateIdentity(targetProject) != nil {
		return nil, ErrConfiguration
	}
	if ctx.Err() != nil {
		return nil, ErrVerificationTemporary
	}
	data, err := deployment.ReadSecret(policy.Path)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ErrVerificationTemporary
		}
		return nil, ErrConfiguration
	}
	return data, nil
}

// verifyFileCredentials isolates the OAuth exchange for deterministic transport
// tests. Production callers always use verifiedHTTP and a fixed ten-second limit.
func verifyFileCredentials(ctx context.Context, policy FileCredentialPolicy, targetProject string, data []byte, timeout time.Duration, testTransport http.RoundTripper) error {
	return withVerificationToken(ctx, policy, targetProject, data, timeout, testTransport, func(string) error { return nil })
}

func withVerificationToken(ctx context.Context, policy FileCredentialPolicy, targetProject string, data []byte, timeout time.Duration, testTransport http.RoundTripper, use func(string) error) error {
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
		defer func() {
			token.Value = ""
			token.Expiry = time.Time{}
		}()
	}
	if err == nil && token != nil {
		return use(token.Value)
	}
	if errors.Is(err, errTokenTemporary) || ctx.Err() != nil {
		return ErrVerificationTemporary
	}
	return ErrCredentials
}
