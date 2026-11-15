//go:build !linux

package provisioning

import "os"

// This store currently requires Linux ownership and ACL inspection semantics.
func trustedRoot(string) bool           { return false }
func trustedEntry(string, bool) bool    { return false }
func setDirectoryOwner(string) error    { return os.ErrPermission }
func setCredentialOwner(*os.File) error { return os.ErrPermission }
