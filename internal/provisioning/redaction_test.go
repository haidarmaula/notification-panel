package provisioning

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

const redactionTestSecret = "synthetic-private-key-sentinel"

func TestStageRequestFormattingAndLoggingRedactCredential(t *testing.T) {
	request := StageRequest{ServiceAccount: json.RawMessage(`{"private_key":"` + redactionTestSecret + `"}`)}
	for _, formatted := range []string{fmt.Sprintf("%v", request), fmt.Sprintf("%+v", request), fmt.Sprintf("%#v", request)} {
		if strings.Contains(formatted, redactionTestSecret) || formatted != redactedValue {
			t.Fatal("StageRequest formatting exposed credential material")
		}
	}
	var output bytes.Buffer
	slog.New(slog.NewTextHandler(&output, nil)).Info("stage", "request", request)
	if strings.Contains(output.String(), redactionTestSecret) {
		t.Fatal("StageRequest structured logging exposed credential material")
	}
}

func TestStageRequestJSONRetainsCredentialWirePayload(t *testing.T) {
	request := StageRequest{ServiceAccount: json.RawMessage(`{"private_key":"` + redactionTestSecret + `"}`)}
	data, err := json.Marshal(request)
	if err != nil || !bytes.Contains(data, []byte(redactionTestSecret)) || !bytes.Contains(data, []byte(`"service_account"`)) {
		t.Fatal("StageRequest JSON no longer contains the stage wire payload")
	}
}

func TestPolicyFormattingLoggingAndJSONRedactSettings(t *testing.T) {
	policy := Policy{identity: redactionTestSecret, listenAddress: "127.0.0.1:9090"}
	for _, formatted := range []string{fmt.Sprintf("%v", policy), fmt.Sprintf("%+v", policy), fmt.Sprintf("%#v", policy)} {
		if strings.Contains(formatted, redactionTestSecret) || formatted != redactedValue {
			t.Fatal("Policy formatting exposed deployment settings")
		}
	}
	var output bytes.Buffer
	slog.New(slog.NewTextHandler(&output, nil)).Info("policy", "policy", policy)
	if strings.Contains(output.String(), redactionTestSecret) {
		t.Fatal("Policy structured logging exposed deployment settings")
	}
	data, err := json.Marshal(policy)
	if err != nil || string(data) != `"[REDACTED]"` {
		t.Fatal("Policy JSON serialization did not redact deployment settings")
	}
}
