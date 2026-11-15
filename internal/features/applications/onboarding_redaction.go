package applications

import "log/slog"

func (OnboardingSettings) String() string               { return "[REDACTED]" }
func (v OnboardingSettings) GoString() string           { return v.String() }
func (v OnboardingSettings) LogValue() slog.Value       { return slog.StringValue(v.String()) }
func (OnboardingSettings) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }
func (*OnboardingService) String() string               { return "[REDACTED]" }
func (v *OnboardingService) GoString() string           { return v.String() }
func (v *OnboardingService) LogValue() slog.Value       { return slog.StringValue(v.String()) }
