//go:build !linux && !darwin

package deployment

// ACL trust inspection is currently supported only on Linux and Darwin.
func noExtendedACL(string) bool { return false }
