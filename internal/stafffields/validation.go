// Package stafffields contains validation shared by staff administration and
// self-service profile operations.
package stafffields

import (
	"errors"
	"unicode/utf8"
)

// MaxTextLength is the maximum character count accepted by staff_users name
// and email columns, which are VARCHAR(255).
const MaxTextLength = 255

// ErrTextTooLong indicates that a staff name or email exceeds its database
// column limit.
var ErrTextTooLong = errors.New("name and email must not exceed 255 characters")

// ValidateText checks each supplied value against the character limit. Empty
// values are allowed because update operations use them to mean unchanged.
func ValidateText(name, email string) error {
	if utf8.RuneCountInString(name) > MaxTextLength || utf8.RuneCountInString(email) > MaxTextLength {
		return ErrTextTooLong
	}
	return nil
}
