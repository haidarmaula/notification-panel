package onboarding

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

const testUUID = "11111111-1111-4111-8111-111111111111"

func TestDecodeEnrollmentRequest(t *testing.T) {
	valid := `{"profile_id":"` + testUUID + `","platforms":[{"platform":"ANDROID","application_identifier":"com.example.app"}]}`
	got, err := DecodeEnrollmentRequest(strings.NewReader(valid))
	if err != nil || got.ProfileID != testUUID || len(got.Platforms) != 1 || got.Platforms[0].Platform != PlatformAndroid {
		t.Fatalf("DecodeEnrollmentRequest() = %#v, %v", got, err)
	}
	for name, body := range map[string]string{
		"unknown field":      strings.TrimSuffix(valid, "}") + `,"profileId":"` + testUUID + `"}`,
		"duplicate key":      strings.Replace(valid, `,"platforms":`, `,"profile_id":"`+testUUID+`","platforms":`, 1),
		"case alias":         strings.Replace(valid, `"profile_id"`, `"Profile_ID"`, 1),
		"null field":         strings.Replace(valid, `"profile_id":"`+testUUID+`"`, `"profile_id":null`, 1),
		"duplicate platform": `{"profile_id":"` + testUUID + `","platforms":[{"platform":"IOS","application_identifier":"a"},{"platform":"IOS","application_identifier":"b"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeEnrollmentRequest(strings.NewReader(body)); err == nil {
				t.Fatal("expected invalid request")
			}
		})
	}
}

func TestDecodeVerifyRequestNullableOptionalID(t *testing.T) {
	for _, body := range []string{
		`{"configuration_id":"` + testUUID + `"}`,
		`{"configuration_id":"` + testUUID + `","test_installation_id":"` + testUUID + `"}`,
	} {
		if _, err := DecodeVerifyRequest(strings.NewReader(body)); err != nil {
			t.Fatalf("DecodeVerifyRequest(%s): %v", body, err)
		}
	}
	for _, body := range []string{
		`{"configuration_id":"` + testUUID + `","test_installation_id":null}`,
		`{"configuration_id":"` + testUUID + `","test_installation_id":42}`,
		`{"configuration_id":"` + testUUID + `","Test_Installation_ID":null}`,
	} {
		if _, err := DecodeVerifyRequest(strings.NewReader(body)); err == nil {
			t.Fatalf("DecodeVerifyRequest(%s): expected error", body)
		}
	}
}

func TestDecodeStrictRequestShapes(t *testing.T) {
	tests := []struct {
		name   string
		decode func(*strings.Reader) error
		body   string
	}{
		{"configuration", func(r *strings.Reader) error { _, err := DecodeConfigurationRequest(r); return err }, `{"configuration_id":"` + testUUID + `"}`},
		{"cohort", func(r *strings.Reader) error { _, err := DecodeTestCohortRequest(r); return err }, `{"installation_ids":["` + testUUID + `"],"reason":"test","evidence_id":"` + testUUID + `"}`},
		{"release", func(r *strings.Reader) error { _, err := DecodeReleaseRequest(r); return err }, `{"mode":"PILOT","cohort_id":"` + testUUID + `","evidence_id":"` + testUUID + `"}`},
		{"hold", func(r *strings.Reader) error { _, err := DecodeHoldRequest(r); return err }, `{"reason":"hold"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.decode(strings.NewReader(test.body)); err != nil {
				t.Fatalf("valid request rejected: %v", err)
			}
		})
	}
	if _, err := DecodeReleaseRequest(strings.NewReader(`{"mode":"pilot","cohort_id":"` + testUUID + `","evidence_id":"` + testUUID + `"}`)); err == nil {
		t.Fatal("accepted noncanonical enum")
	}
	if _, err := DecodeTestCohortRequest(strings.NewReader(`{"installation_ids":["` + testUUID + `","` + testUUID + `"],"reason":"test","evidence_id":"` + testUUID + `"}`)); err == nil {
		t.Fatal("accepted duplicate installation ID")
	}
}

func TestDecodeFCMRequestStrictlyValidatesNestedCredential(t *testing.T) {
	serviceAccount := `{"type":"service_account","project_id":"provider-project","private_key_id":"key-id","private_key":"secret-marker","client_email":"svc@example.com","token_uri":"https://oauth2.googleapis.com/token"}`
	valid := `{"environment":"staging","target_project_id":"provider-project","service_account":` + serviceAccount + `}`
	got, err := DecodeFCMRequest(strings.NewReader(valid))
	if err != nil || len(got.ServiceAccount) == 0 {
		t.Fatalf("DecodeFCMRequest() = %#v, %v", got, err)
	}
	badNested := strings.Replace(serviceAccount, `"private_key_id"`, `"PRIVATE_KEY_ID"`, 1)
	badDuplicate := strings.Replace(serviceAccount, `,"private_key":`, `,"private_key_id":"second","private_key":`, 1)
	for name, account := range map[string]string{
		"nested alias":     badNested,
		"nested duplicate": badDuplicate,
		"nested unknown":   strings.TrimSuffix(serviceAccount, "}") + `,"token":"secret"}`,
	} {
		t.Run(name, func(t *testing.T) {
			input := `{"environment":"staging","target_project_id":"provider-project","service_account":` + account + `}`
			_, err := DecodeFCMRequest(strings.NewReader(input))
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("error = %v; wanted a generic safe error", err)
			}
		})
	}
}

func TestDecodeRequestLimitsAndJSONFraming(t *testing.T) {
	for name, body := range map[string][]byte{
		"oversized":       bytes.Repeat([]byte(" "), requestLimit+1),
		"trailing object": []byte(`{"reason":"hold"} {}`),
		"nonobject":       []byte(`null`),
		"invalid UTF-8":   {'{', '"', 'r', 'e', 'a', 's', 'o', 'n', '"', ':', '"', 0xff, '"', '}'},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeHoldRequest(bytes.NewReader(body)); err == nil {
				t.Fatal("expected invalid request")
			}
		})
	}
	if _, err := DecodeHoldRequest(bytes.NewReader(bytes.Repeat([]byte(" "), requestLimit+1))); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized request error = %v; want ErrTooLarge", err)
	}
}

func TestDecodersRejectNilReaders(t *testing.T) {
	if _, err := DecodeFCMRequest(nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("DecodeFCMRequest(nil) error = %v", err)
	}
	if _, err := DecodeEnrollmentRequest(nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("DecodeEnrollmentRequest(nil) error = %v", err)
	}
	if _, err := DecodeVerifyRequest(nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("DecodeVerifyRequest(nil) error = %v", err)
	}
	if _, err := DecodeConfigurationRequest(nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("DecodeConfigurationRequest(nil) error = %v", err)
	}
	if _, err := DecodeTestCohortRequest(nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("DecodeTestCohortRequest(nil) error = %v", err)
	}
	if _, err := DecodeReleaseRequest(nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("DecodeReleaseRequest(nil) error = %v", err)
	}
	if _, err := DecodeHoldRequest(nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("DecodeHoldRequest(nil) error = %v", err)
	}
}

func TestDecodeFCMRequestLimits(t *testing.T) {
	tooLarge := `{"environment":"staging","target_project_id":"p","service_account":{}}` + strings.Repeat(" ", fcmRequestLimit)
	if _, err := DecodeFCMRequest(strings.NewReader(tooLarge)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("outer overflow error = %v; want ErrTooLarge", err)
	}
	account := `{"type":"service_account","project_id":"p","private_key_id":"id","private_key":"` + strings.Repeat("x", serviceAccountLimit) + `","client_email":"svc@example.com","token_uri":"https://oauth2.googleapis.com/token"}`
	body := `{"environment":"staging","target_project_id":"p","service_account":` + account + `}`
	if int64(len(body)) > fcmRequestLimit {
		t.Fatal("test fixture exceeds the outer request limit")
	}
	if _, err := DecodeFCMRequest(strings.NewReader(body)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("nested overflow error = %v; want ErrTooLarge", err)
	}
}
