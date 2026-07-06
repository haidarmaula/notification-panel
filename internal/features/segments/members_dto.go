package segments

// AddMemberRequest represents request payload for adding a user to a segment.
type AddMemberRequest struct {
	UserID int64 `json:"user_id"`
}

// MemberListItem represents a segment member in a list view.
type MemberListItem struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"user_id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	CreatedAt string `json:"created_at"`
}

// ListMembersResponse represents paginated list of segment members.
type ListMembersResponse struct {
	Data       []MemberListItem `json:"data"`
	Pagination Pagination       `json:"pagination"`
}
