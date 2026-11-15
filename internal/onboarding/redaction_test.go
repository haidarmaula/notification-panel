package onboarding

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestCredentialObjectsRedactFormattingAndStructuredLogs(t *testing.T) {
	const sensitive = "private-material-fixture"
	for _, value := range []any{FCMRequest{ServiceAccount: json.RawMessage(`{"private_key":"` + sensitive + `"}`)}, ServerPolicy{ClientPrivateKey: []byte(sensitive)}, VerifierPolicy{DatabaseURL: sensitive}} {
		if strings.Contains(fmt.Sprintf("%+v %#v", value, value), sensitive) {
			t.Fatal("credential formatting leaked private material")
		}
		logged := slog.AnyValue(value).Resolve()
		if logged.String() != "[REDACTED]" {
			t.Fatal("credential log value was not redacted")
		}
	}
	for _, value := range []any{ServerPolicy{ClientPrivateKey: []byte(sensitive)}, VerifierPolicy{DatabaseURL: sensitive}} {
		data, err := json.Marshal(value)
		if err != nil || string(data) != `"[REDACTED]"` {
			t.Fatal("startup policy JSON was not redacted")
		}
	}
	data, err := json.Marshal(FCMRequest{ServiceAccount: json.RawMessage(`{"private_key":"` + sensitive + `"}`)})
	if err != nil || !strings.Contains(string(data), sensitive) {
		t.Fatal("write-only request wire encoding was broken")
	}
}
