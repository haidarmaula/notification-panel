//go:build !linux && !darwin

package requeue

// ACL trust inspection is currently supported only on Linux and Darwin.
func noExtendedACL(string) bool { return false }
