//go:build integration && linux

package fcm

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"hello/internal/deployment"
)

// Run only in a disposable root-owned container with --network none. Never
// place generated test keys in the host's real deployment secret directory.
func TestProtectedFileClientConstruction(t *testing.T) {
	if os.Getenv("HELLO_PROTECTED_FILE_TEST") != "1" {
		t.Skip("requires disposable root filesystem")
	}
	if os.Geteuid() != 0 {
		t.Fatal("protected-file fixture requires container root")
	}
	if err := os.MkdirAll("/etc/hello", 0750); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp("/etc/hello", "credential-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	data, policy, _ := fileFixture(t)
	config, _ := transportFixture(t)
	policy.Path = filepath.Join(directory, "service-account.json")
	if err := os.WriteFile(policy.Path, data, 0640); err != nil {
		t.Fatal(err)
	}
	if _, err := deployment.ReadSecret(policy.Path); err != nil {
		for _, path := range []string{"/", "/etc", "/etc/hello", directory, policy.Path} {
			info, statErr := os.Lstat(path)
			_, accessErr := syscall.Getxattr(path, "system.posix_acl_access", nil)
			_, defaultErr := syscall.Getxattr(path, "system.posix_acl_default", nil)
			if statErr == nil {
				t.Logf("fixture entry mode=%v uid=%d access=%v default=%v", info.Mode(), info.Sys().(*syscall.Stat_t).Uid, accessErr, defaultErr)
			}
		}
		t.Fatal("protected secret reader rejected fixture")
	}
	if _, cleanup, err := fileSource(config, policy, data, nil); err != nil {
		t.Fatal("local credential provider rejected fixture")
	} else {
		cleanup()
	}
	client, err := NewFile(context.Background(), config, policy)
	if err != nil {
		t.Fatal("protected valid key rejected")
	}
	client.Close()
	if err := os.Chmod(policy.Path, 0660); err != nil {
		t.Fatal(err)
	}
	if client, err := NewFile(context.Background(), config, policy); client != nil || err != ErrConfiguration {
		t.Fatal("writable key accepted")
	}
	if err := os.Chmod(policy.Path, 0640); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(directory, "alias.json")
	if err := os.Symlink(policy.Path, alias); err != nil {
		t.Fatal(err)
	}
	policy.Path = alias
	if client, err := NewFile(context.Background(), config, policy); client != nil || err != ErrConfiguration {
		t.Fatal("symlink key accepted")
	}
}
