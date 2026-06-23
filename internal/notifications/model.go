package notifications

type Hello struct {
	Message string `json:"message"`
}

type Notification struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Message string `json:"message"`
}
