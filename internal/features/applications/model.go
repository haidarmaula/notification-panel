// Package applications manages provisioning registry metadata and explicit staff
// grants. It does not activate projects or scope the legacy business pipeline.
package applications

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"hello/internal/database/sqlc"
)

var (
	ErrInvalidKey           = errors.New("invalid application key")
	ErrInvalidName          = errors.New("invalid application name")
	ErrInvalidID            = errors.New("invalid application id")
	ErrInvalidStaffID       = errors.New("invalid staff id")
	ErrInvalidPagination    = errors.New("invalid pagination")
	ErrKeyExists            = errors.New("application key already exists")
	ErrStaffNotFound        = errors.New("staff user not found")
	ErrStaffInactive        = errors.New("staff user is inactive")
	ErrGrantNotFound        = errors.New("application grant not found")
	ErrInvalidPrecondition  = errors.New("invalid precondition")
	ErrPreconditionRequired = errors.New("precondition required")
	ErrPreconditionFailed   = errors.New("precondition failed")
)
var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,61}[a-z0-9]$`)

func validateKey(key string) error {
	if !keyPattern.MatchString(key) {
		return ErrInvalidKey
	}
	return nil
}
func cleanName(name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", ErrInvalidName
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", ErrInvalidName
		}
	}
	name = strings.TrimSpace(name)
	if len(name) == 0 || len(name) > 128 {
		return "", ErrInvalidName
	}
	return name, nil
}
func pageOffset(page, limit int32) (int32, error) {
	if page < 1 || page > 1000 || limit < 1 || limit > 100 {
		return 0, ErrInvalidPagination
	}
	return (page - 1) * limit, nil
}
func validID(id pgtype.UUID) error {
	if !id.Valid {
		return ErrInvalidID
	}
	return nil
}

// Preconditions model supported HTTP conditions without trusting client revision
// data. Compare exact server-built strong tags after current authority checks.
type Preconditions struct{ IfMatch, IfNoneMatch string }

func validStrongTag(tag string) bool {
	if len(tag) < 3 || tag[0] != '"' || tag[len(tag)-1] != '"' {
		return false
	}
	for _, b := range []byte(tag[1 : len(tag)-1]) {
		if b < 0x21 || b > 0x7e || b == '"' {
			return false
		}
	}
	return true
}
func (c Preconditions) validate(allowCreate bool) error {
	if c.IfMatch == "" && c.IfNoneMatch == "" {
		return ErrPreconditionRequired
	}
	if c.IfMatch != "" && c.IfNoneMatch != "" {
		return ErrInvalidPrecondition
	}
	if c.IfNoneMatch != "" {
		if !allowCreate || c.IfNoneMatch != "*" {
			return ErrInvalidPrecondition
		}
		return nil
	}
	if !validStrongTag(c.IfMatch) {
		return ErrInvalidPrecondition
	}
	return nil
}
func ApplicationETag(app sqlc.Application) string {
	return fmt.Sprintf(`"app:%s:%d"`, app.PublicID.String(), app.Revision)
}
func GrantETag(app sqlc.Application, g sqlc.ApplicationStaffGrant) string {
	return fmt.Sprintf(`"grant:%s:%d:%d"`, app.PublicID.String(), g.StaffID, g.Revision)
}

type GrantResult struct {
	Grant   sqlc.ApplicationStaffGrant
	Created bool
}
