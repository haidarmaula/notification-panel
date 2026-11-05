//go:build darwin

package requeue

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"testing"
)

func TestDarwinDeploymentACLIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	// Temp parents may carry inherited ACLs; normalize this owned fixture first.
	if out, err := exec.Command("/bin/chmod", "-N", path).CombinedOutput(); err != nil {
		t.Fatalf("fixture ACL reset failed: %v %s", err, out)
	}
	if !noExtendedACL(path) {
		t.Fatal("rejected empty ACL")
	}
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	principal := "user:" + account.Username + " allow writesecurity"
	if out, err := exec.Command("/bin/chmod", "+a", principal, path).CombinedOutput(); err != nil {
		t.Fatalf("fixture ACL creation failed: %v %s", err, out)
	}
	if noExtendedACL(path) {
		t.Fatal("accepted extended ACL write-security permission")
	}
}
