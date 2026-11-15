package provisioning

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const testAppID = "11111111-1111-4111-8111-111111111111"
const testConfigID = "22222222-2222-4222-8222-222222222222"

func TestStageRejectsInvalidInputWithoutReturningSecret(t *testing.T) {
	cases := []struct {
		value string
		valid bool
	}{
		{testAppID, true},
		{testConfigID, true},
		{"00000000-0000-0000-0000-000000000000", false},
		{"../" + testAppID, false},
		{"11111111-1111-4111-8111-11111111111A", false},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			if got := canonicalUUID(tc.value); got != tc.valid {
				t.Fatalf("canonicalUUID(%q) = %t, want %t", tc.value, got, tc.valid)
			}
		})
	}
}

func TestNewFileStoreRejectsUntrustedRoot(t *testing.T) {
	if _, err := NewFileStore(filepath.Join(t.TempDir(), "store")); err == nil {
		t.Fatal("NewFileStore accepted an untrusted, missing root")
	}
}

func TestStagePublishesImmutableVersion(t *testing.T) {
	store, root := rootOwnedStore(t)
	data := serviceAccountFixture(t)
	first, err := store.Stage(context.Background(), testAppID, "production", testConfigID, "fixture-project", "fixture@fixture-project.iam.gserviceaccount.com", data)
	if err != nil {
		t.Fatalf("Stage failed: %v", err)
	}
	if first.Reference != testConfigID || first.Size != int64(len(data)) {
		t.Fatalf("unexpected safe metadata: %+v", first)
	}
	path := filepath.Join(root, testAppID, "production", testConfigID, "service-account.json")
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal("published credential is missing")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0640 {
		t.Fatalf("unexpected credential mode: %v", info.Mode())
	}
	if again, err := store.Stage(context.Background(), testAppID, "production", testConfigID, "fixture-project", "fixture@fixture-project.iam.gserviceaccount.com", data); err != nil || again != first {
		t.Fatalf("same bytes did not converge: result=%+v err=%v", again, err)
	}
	changed := append(append([]byte(nil), data...), '\n')
	if _, err := store.Stage(context.Background(), testAppID, "production", testConfigID, "fixture-project", "fixture@fixture-project.iam.gserviceaccount.com", changed); err == nil {
		t.Fatal("different bytes replaced an immutable version")
	}
}

func TestStageRejectsSymlinkedApplicationDirectory(t *testing.T) {
	store, root := rootOwnedStore(t)
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0750); err != nil {
		t.Fatal("cannot create test directory")
	}
	if err := os.Chown(outside, 0, workerGroupID); err != nil {
		t.Fatal("cannot prepare protected test directory")
	}
	if err := os.Symlink(outside, filepath.Join(root, testAppID)); err != nil {
		t.Fatal("cannot create test symlink")
	}
	if _, err := store.Stage(context.Background(), testAppID, "staging", testConfigID, "fixture-project", "fixture@fixture-project.iam.gserviceaccount.com", serviceAccountFixture(t)); err == nil {
		t.Fatal("Stage followed a symlink")
	}
}

func rootOwnedStore(t *testing.T) (*FileStore, string) {
	t.Helper()
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("protected publication requires Linux root ownership")
	}
	root, err := os.MkdirTemp("/var/lib", "hello-provisioning-test-")
	if err != nil {
		t.Skip("cannot create protected test root")
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.Chmod(root, 0750); err != nil {
		t.Fatal("cannot prepare protected test root")
	}
	if err := os.Chown(root, 0, workerGroupID); err != nil {
		t.Fatal("cannot prepare protected test root")
	}
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatal("cannot construct store")
	}
	return store, root
}

func serviceAccountFixture(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal("cannot create synthetic key")
	}
	encodedKey, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal("cannot encode synthetic key")
	}
	privateKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey})
	data, err := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "fixture-project", "private_key_id": "synthetic-kid",
		"private_key": string(privateKey), "client_email": "fixture@fixture-project.iam.gserviceaccount.com",
		"token_uri": "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		t.Fatal("cannot encode synthetic credential")
	}
	return data
}
