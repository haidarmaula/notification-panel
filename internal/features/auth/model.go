package auth

// User represents a staff user in the authentication context.
// This is a minimal view used for login and token generation.
type User struct {
	ID       int64  `json:"id"`
	Email    string `json:"email"`
	Password string `json:"password"`
}
