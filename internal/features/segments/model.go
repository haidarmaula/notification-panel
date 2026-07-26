package segments

import "time"

type Actor struct {
	ID   int64
	Name string
}

// Segment represents the domain model for a user segment.
type Segment struct {
	ID          int64
	Name        string
	Description string
	CreatedBy   Actor
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// SegmentWithCount extends Segment with computed member count.
type SegmentWithCount struct {
	Segment
	MemberCount int64
}

// Member represents a segment member domain model.
type Member struct {
	ID        int64
	UserID    int64
	Name      string
	Email     string
	CreatedAt time.Time
}
