package notifications

// NotificationStatus defines notification status.
type NotificationStatus string

const (
	StatusDraft     NotificationStatus = "DRAFT"
	StatusScheduled NotificationStatus = "SCHEDULED"
	StatusSending   NotificationStatus = "SENDING"
	StatusSent      NotificationStatus = "SENT"
	StatusFailed    NotificationStatus = "FAILED"
)

// TargetType defines target type.
type TargetType string

const (
	TargetBroadcast  TargetType = "BROADCAST"
	TargetSegment    TargetType = "SEGMENT"
	TargetIndividual TargetType = "INDIVIDUAL"
)
