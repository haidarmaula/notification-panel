package provisioning

import (
	"encoding/json"
	"log/slog"
)

const redactedValue = "[REDACTED]"

func (StageRequest) String() string   { return redactedValue }
func (StageRequest) GoString() string { return redactedValue }
func (StageRequest) LogValue() slog.Value {
	return slog.StringValue(redactedValue)
}

// MarshalJSON preserves the credential-bearing stage wire payload. Callers
// must treat its output as secret and must not log it.
func (request StageRequest) MarshalJSON() ([]byte, error) {
	type stageRequestWire StageRequest
	return json.Marshal(stageRequestWire(request))
}

func (Policy) String() string   { return redactedValue }
func (Policy) GoString() string { return redactedValue }
func (Policy) LogValue() slog.Value {
	return slog.StringValue(redactedValue)
}

func (Policy) MarshalJSON() ([]byte, error) {
	return json.Marshal(redactedValue)
}
