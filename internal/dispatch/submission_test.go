package dispatch

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestSubmissionDataRequiresStringObject(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"value":null}`, `{"value":3}`, `{"value":{"nested":true}}`} {
		t.Run(raw, func(t *testing.T) {
			if _, err := submissionData([]byte(raw)); err != ErrExecutionHeld {
				t.Fatal("non-string data accepted")
			}
		})
	}
	data, err := submissionData([]byte(`{"value":""}`))
	if err != nil || len(data) != 1 || data["value"] != "" {
		t.Fatal("valid empty string rejected")
	}
}
func TestExecutionProofFormattingRedactsCopies(t *testing.T) {
	for _, proof := range []any{ExecutionLease{}, &ExecutionLease{}, AttemptReservation{}, &AttemptReservation{}} {
		raw, err := json.Marshal(proof)
		if err != nil || string(raw) != `"[REDACTED]"` || fmt.Sprintf("%+v", proof) != "[REDACTED]" || fmt.Sprintf("%#v", proof) != "[REDACTED]" {
			t.Fatal("proof formatting is not redacted")
		}
	}
}
