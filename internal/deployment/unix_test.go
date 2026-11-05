//go:build unix

package deployment

import (
	"os"
	"syscall"
	"testing"
	"time"
)

type deploymentInfo struct {
	mode os.FileMode
	uid  uint32
	size int64
}

func (i deploymentInfo) Name() string       { return "requeue.json" }
func (i deploymentInfo) Size() int64        { return i.size }
func (i deploymentInfo) Mode() os.FileMode  { return i.mode }
func (i deploymentInfo) ModTime() time.Time { return time.Time{} }
func (i deploymentInfo) IsDir() bool        { return i.mode.IsDir() }
func (i deploymentInfo) Sys() any           { return &syscall.Stat_t{Uid: i.uid} }
func TestTrustedDeploymentEntry(t *testing.T) {
	for _, tt := range []struct {
		name            string
		info            deploymentInfo
		directory, want bool
	}{
		{"root private file", deploymentInfo{0600, 0, 100}, false, true},
		{"root group-readable file", deploymentInfo{0640, 0, 100}, false, true},
		{"untrusted owner", deploymentInfo{0600, 1000, 100}, false, false},
		{"world-readable secret", deploymentInfo{0644, 0, 100}, false, false},
		{"group-writable trust", deploymentInfo{0660, 0, 100}, false, false},
		{"executable config", deploymentInfo{0700, 0, 100}, false, false},
		{"symlink", deploymentInfo{os.ModeSymlink | 0600, 0, 100}, false, false},
		{"named pipe", deploymentInfo{os.ModeNamedPipe | 0600, 0, 100}, false, false},
		{"oversized file", deploymentInfo{0600, 0, 64*1024 + 1}, false, false},
		{"trusted directory", deploymentInfo{os.ModeDir | 0755, 0, 0}, true, true},
		{"writable directory", deploymentInfo{os.ModeDir | 0775, 0, 0}, true, false},
		{"sticky temp directory", deploymentInfo{os.ModeDir | os.ModeSticky | 0777, 0, 0}, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := trustedDeploymentEntry(tt.info, tt.directory); got != tt.want {
				t.Fatalf("trusted=%v want %v", got, tt.want)
			}
		})
	}
}
func TestDeploymentFileRejectsUntrustedPaths(t *testing.T) {
	for _, path := range []string{"relative.json", "/etc/hello/../requeue.json", t.TempDir() + "/requeue.json"} {
		if cfg, err := readFile(path); cfg != nil || err != ErrConfiguration {
			t.Fatal("accepted untrusted deployment path")
		}
	}
}
