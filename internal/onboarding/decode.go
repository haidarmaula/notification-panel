package onboarding

import (
	"encoding/json"
	"errors"
	"io"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/jsonobject"
)

const (
	requestLimit        = 64 * 1024
	fcmRequestLimit     = 96 * 1024
	serviceAccountLimit = 64 * 1024
)

// ErrInvalidRequest is intentionally generic so malformed input is never echoed to callers.
var ErrInvalidRequest = errors.New("invalid onboarding request")

// ErrTooLarge identifies a request or nested credential that exceeds its byte limit.
var ErrTooLarge = errors.New("onboarding request too large")

// DecodeFCMRequest decodes a bounded FCM credential request and strictly checks its nested service account object.
func DecodeFCMRequest(reader io.Reader) (FCMRequest, error) {
	var request FCMRequest
	data, err := readRequest(reader, fcmRequestLimit)
	if err != nil {
		return FCMRequest{}, err
	}
	if jsonobject.Decode(data, map[string]any{
		"environment":       &request.Environment,
		"target_project_id": &request.TargetProjectID,
		"service_account":   &request.ServiceAccount,
	}) != nil || !validEnvironment(request.Environment) || !validText(request.TargetProjectID, 128) || len(request.ServiceAccount) == 0 {
		return FCMRequest{}, ErrInvalidRequest
	}
	if len(request.ServiceAccount) > serviceAccountLimit {
		return FCMRequest{}, ErrTooLarge
	}
	if !validServiceAccount(request.ServiceAccount) {
		return FCMRequest{}, ErrInvalidRequest
	}
	return request, nil
}

// DecodeEnrollmentRequest decodes a strict enrollment profile and platform mapping.
func DecodeEnrollmentRequest(reader io.Reader) (EnrollmentRequest, error) {
	var request EnrollmentRequest
	var platforms []json.RawMessage
	data, err := readRequest(reader, requestLimit)
	if err != nil {
		return EnrollmentRequest{}, err
	}
	if jsonobject.Decode(data, map[string]any{"profile_id": &request.ProfileID, "platforms": &platforms}) != nil ||
		!validUUID(request.ProfileID) || len(platforms) < 1 || len(platforms) > 2 {
		return EnrollmentRequest{}, ErrInvalidRequest
	}
	request.Platforms = make([]EnrollmentPlatform, 0, len(platforms))
	seen := make(map[Platform]struct{}, len(platforms))
	for _, raw := range platforms {
		var item EnrollmentPlatform
		if jsonobject.Decode(raw, map[string]any{"platform": &item.Platform, "application_identifier": &item.ApplicationIdentifier}) != nil ||
			!validPlatform(item.Platform) || !validText(item.ApplicationIdentifier, 255) {
			return EnrollmentRequest{}, ErrInvalidRequest
		}
		if _, exists := seen[item.Platform]; exists {
			return EnrollmentRequest{}, ErrInvalidRequest
		}
		seen[item.Platform] = struct{}{}
		request.Platforms = append(request.Platforms, item)
	}
	return request, nil
}

// DecodeVerifyRequest decodes a strict candidate verification request.
func DecodeVerifyRequest(reader io.Reader) (VerifyRequest, error) {
	var request VerifyRequest
	data, err := readRequest(reader, requestLimit)
	if err != nil {
		return VerifyRequest{}, err
	}
	if !jsonobject.UniqueLowercaseFields(data) {
		return VerifyRequest{}, ErrInvalidRequest
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil || len(fields) < 1 || len(fields) > 2 {
		return VerifyRequest{}, ErrInvalidRequest
	}
	for key := range fields {
		if key != "configuration_id" && key != "test_installation_id" {
			return VerifyRequest{}, ErrInvalidRequest
		}
	}
	if json.Unmarshal(fields["configuration_id"], &request.ConfigurationID) != nil || !validUUID(request.ConfigurationID) {
		return VerifyRequest{}, ErrInvalidRequest
	}
	installation, present := fields["test_installation_id"]
	if present {
		var value string
		if json.Unmarshal(installation, &value) != nil || !validUUID(value) {
			return VerifyRequest{}, ErrInvalidRequest
		}
		request.TestInstallationID = &value
	}
	return request, nil
}

// DecodeConfigurationRequest decodes a strict configuration ID request.
func DecodeConfigurationRequest(reader io.Reader) (ConfigurationRequest, error) {
	var request ConfigurationRequest
	data, err := readRequest(reader, requestLimit)
	if err != nil {
		return ConfigurationRequest{}, err
	}
	if jsonobject.Decode(data, map[string]any{"configuration_id": &request.ConfigurationID}) != nil || !validUUID(request.ConfigurationID) {
		return ConfigurationRequest{}, ErrInvalidRequest
	}
	return request, nil
}

// DecodeTestCohortRequest decodes a bounded installation cohort and its evidence.
func DecodeTestCohortRequest(reader io.Reader) (TestCohortRequest, error) {
	var request TestCohortRequest
	data, err := readRequest(reader, requestLimit)
	if err != nil {
		return TestCohortRequest{}, err
	}
	if jsonobject.Decode(data, map[string]any{
		"installation_ids": &request.InstallationIDs,
		"reason":           &request.Reason,
		"evidence_id":      &request.EvidenceID,
	}) != nil || len(request.InstallationIDs) < 1 || len(request.InstallationIDs) > 10 ||
		!validText(request.Reason, 256) || !validUUID(request.EvidenceID) {
		return TestCohortRequest{}, ErrInvalidRequest
	}
	seen := make(map[string]struct{}, len(request.InstallationIDs))
	for _, id := range request.InstallationIDs {
		if !validUUID(id) {
			return TestCohortRequest{}, ErrInvalidRequest
		}
		if _, exists := seen[id]; exists {
			return TestCohortRequest{}, ErrInvalidRequest
		}
		seen[id] = struct{}{}
	}
	return request, nil
}

// DecodeReleaseRequest decodes a strict dispatch release request.
func DecodeReleaseRequest(reader io.Reader) (ReleaseRequest, error) {
	var request ReleaseRequest
	data, err := readRequest(reader, requestLimit)
	if err != nil {
		return ReleaseRequest{}, err
	}
	if jsonobject.Decode(data, map[string]any{
		"mode":        &request.Mode,
		"cohort_id":   &request.CohortID,
		"evidence_id": &request.EvidenceID,
	}) != nil || (request.Mode != ReleasePilot && request.Mode != ReleaseFull) ||
		!validUUID(request.CohortID) || !validUUID(request.EvidenceID) {
		return ReleaseRequest{}, ErrInvalidRequest
	}
	return request, nil
}

// DecodeHoldRequest decodes a strict dispatch hold reason.
func DecodeHoldRequest(reader io.Reader) (HoldRequest, error) {
	var request HoldRequest
	data, err := readRequest(reader, requestLimit)
	if err != nil {
		return HoldRequest{}, err
	}
	if jsonobject.Decode(data, map[string]any{"reason": &request.Reason}) != nil || !validText(request.Reason, 256) {
		return HoldRequest{}, ErrInvalidRequest
	}
	return request, nil
}

func readRequest(reader io.Reader, limit int64) ([]byte, error) {
	if reader == nil {
		return nil, ErrInvalidRequest
	}
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}
	if err != nil || !utf8.Valid(data) {
		return nil, ErrInvalidRequest
	}
	return data, nil
}

func validServiceAccount(data []byte) bool {
	var account struct {
		Type                    string `json:"type"`
		ProjectID               string `json:"project_id"`
		PrivateKeyID            string `json:"private_key_id"`
		PrivateKey              string `json:"private_key"`
		ClientEmail             string `json:"client_email"`
		TokenURI                string `json:"token_uri"`
		AuthURI                 string `json:"auth_uri"`
		ClientID                string `json:"client_id"`
		AuthProviderX509CertURL string `json:"auth_provider_x509_cert_url"`
		ClientX509CertURL       string `json:"client_x509_cert_url"`
		UniverseDomain          string `json:"universe_domain"`
	}
	if jsonobject.DecodeOptional(data,
		map[string]any{
			"type": &account.Type, "project_id": &account.ProjectID, "private_key_id": &account.PrivateKeyID,
			"private_key": &account.PrivateKey, "client_email": &account.ClientEmail, "token_uri": &account.TokenURI,
		},
		map[string]any{
			"auth_uri": &account.AuthURI, "client_id": &account.ClientID,
			"auth_provider_x509_cert_url": &account.AuthProviderX509CertURL,
			"client_x509_cert_url":        &account.ClientX509CertURL, "universe_domain": &account.UniverseDomain,
		}) != nil {
		return false
	}
	address, emailErr := mail.ParseAddress(account.ClientEmail)
	return account.Type == "service_account" && validText(account.ProjectID, 128) && validNonEmpty(account.PrivateKeyID) &&
		validNonEmpty(account.PrivateKey) && emailErr == nil && address.Address == account.ClientEmail &&
		account.TokenURI == "https://oauth2.googleapis.com/token" &&
		(account.UniverseDomain == "" || account.UniverseDomain == "googleapis.com")
}

func validUUID(value string) bool {
	var id pgtype.UUID
	return len(value) == 36 && id.Scan(value) == nil && id.Valid && id.Bytes != [16]byte{} && id.String() == value
}

func validEnvironment(value Environment) bool {
	return value == EnvironmentStaging || value == EnvironmentProduction
}

func validPlatform(value Platform) bool {
	return value == PlatformAndroid || value == PlatformIOS
}

func validText(value string, max int) bool {
	return len(value) > 0 && len(value) <= max && strings.TrimSpace(value) != ""
}

func validNonEmpty(value string) bool { return value != "" }
