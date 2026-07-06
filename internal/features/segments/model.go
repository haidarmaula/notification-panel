package segments

import "time"

// Segment represents the domain model for a user segment.
type Segment struct {
	ID          int64
	Name        string
	Description *string
	CreatedBy   int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
