// Package deployment validates immutable per-process database and broker policy
// and reads protected deployment files without environment or path overrides.
package deployment

import (
	"errors"
	"strings"
	"unicode"
)

// ErrConfiguration never includes credentials, paths or decoder input.
var ErrConfiguration = errors.New("invalid deployment configuration")

// Component identifies one compiled executable's protected policy file.
type Component string

const (
	Server    Component = "server"
	Bootstrap Component = "bootstrap"
	Worker    Component = "worker"
	Scheduler Component = "scheduler"
	Requeue   Component = "requeue"
)

func validPolicyText(value string) bool {
	return len(value) > 0 && len(value) <= 255 && strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}
