//go:build linux

package deployment

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestLinuxDeploymentACLIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if !noExtendedACL(path) {
		t.Fatal("rejected empty Linux ACL")
	}
	// Linux posix_acl_xattr ABI: version 2, then tag/permission/ID entries. The
	// named user's grant is masked read-only; this policy still rejects the ACL.
	acl := make([]byte, 4+5*8)
	binary.LittleEndian.PutUint32(acl, 2)
	for i, entry := range []struct {
		tag, permission uint16
		id              uint32
	}{{1, 6, ^uint32(0)}, {2, 6, 1000}, {4, 4, ^uint32(0)}, {16, 4, ^uint32(0)}, {32, 0, ^uint32(0)}} {
		at := 4 + i*8
		binary.LittleEndian.PutUint16(acl[at:], entry.tag)
		binary.LittleEndian.PutUint16(acl[at+2:], entry.permission)
		binary.LittleEndian.PutUint32(acl[at+4:], entry.id)
	}
	if err := syscall.Setxattr(path, "system.posix_acl_access", acl, 0); err != nil {
		t.Fatal(err)
	}
	if noExtendedACL(path) {
		t.Fatal("accepted extended Linux ACL")
	}
}
