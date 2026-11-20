package onboarding

import "log/slog"

func (CohortTargetClaim) String() string               { return "[REDACTED]" }
func (v CohortTargetClaim) GoString() string           { return v.String() }
func (v CohortTargetClaim) LogValue() slog.Value       { return slog.StringValue(v.String()) }
func (CohortTargetClaim) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }

func (FCMRequest) String() string                     { return "[REDACTED]" }
func (v FCMRequest) GoString() string                 { return v.String() }
func (v FCMRequest) LogValue() slog.Value             { return slog.StringValue(v.String()) }
func (ServerPolicy) String() string                   { return "[REDACTED]" }
func (v ServerPolicy) GoString() string               { return v.String() }
func (v ServerPolicy) LogValue() slog.Value           { return slog.StringValue(v.String()) }
func (v ServerPolicy) MarshalJSON() ([]byte, error)   { return []byte(`"[REDACTED]"`), nil }
func (VerifierPolicy) String() string                 { return "[REDACTED]" }
func (v VerifierPolicy) GoString() string             { return v.String() }
func (v VerifierPolicy) LogValue() slog.Value         { return slog.StringValue(v.String()) }
func (v VerifierPolicy) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }
