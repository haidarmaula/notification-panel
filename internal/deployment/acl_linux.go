//go:build linux

package deployment

import "syscall"

func noExtendedACL(path string) bool {
	// Reject extended access/default ACLs even when their effective POSIX mask
	// currently disallows writes. Unsupported ACL inspection fails closed too.
	for _, name := range []string{"system.posix_acl_access", "system.posix_acl_default"} {
		_, err := syscall.Getxattr(path, name, nil)
		if err != syscall.ENODATA {
			return false
		}
	}
	return true
}
