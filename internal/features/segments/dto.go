package segments

import "time"

// CreateSegmentRequest represents the request payload for creating a segment.
type CreateSegmentRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

// UpdateSegmentRequest represents the request payload for updating a segment.
type UpdateSegmentRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

// SegmentListItem represents a segment in a list view.
type SegmentListItem struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description *string   `json:"description,omitempty"`
	CreatedBy   string    `json:"created_by"`
	MemberCount int64     `json:"member_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// SegmentDetail represents full segment detail.
type SegmentDetail struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Description *string    `json:"description,omitempty"`
	CreatedBy   StaffBrief `json:"created_by"`
	MemberCount int64      `json:"member_count"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// StaffBrief represents a staff user in segment detail.
type StaffBrief struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// ListSegmentsResponse represents paginated list response.
type ListSegmentsResponse struct {
	Data       []SegmentListItem `json:"data"`
	Pagination Pagination        `json:"pagination"`
}

// Pagination holds pagination metadata.
type Pagination struct {
	Page  int32 `json:"page"`
	Limit int32 `json:"limit"`
	Total int64 `json:"total"`
}

// CreateSegmentResponse represents response after creating a segment.
type CreateSegmentResponse struct {
	ID int64 `json:"id"`
}
