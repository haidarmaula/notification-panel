package provisioning

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"hello/internal/fcm"
)

const maxCredentialSize = 64 * 1024
const workerGroupID = 65532

var errFileStore = errors.New("credential staging failed")

// StageResult contains only identifiers and non-secret file metadata.
type StageResult struct {
	Reference   string `json:"reference"`
	AppID       string `json:"application_id"`
	Environment string `json:"environment"`
	ConfigID    string `json:"configuration_id"`
	Size        int64  `json:"size"`
}

// FileStore publishes immutable service-account files below a trusted root.
type FileStore struct {
	root string
}

// NewFileStore accepts a fixed, absolute host path. The root must already exist
// and satisfy the platform's root ownership, mode and ACL policy.
func NewFileStore(root string) (*FileStore, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || strings.ContainsRune(root, '\x00') || !trustedRoot(root) {
		return nil, errFileStore
	}
	return &FileStore{root: root}, nil
}

// Stage validates and durably publishes one immutable credential version.
// Repeating the same version with the same bytes converges successfully.
func (s *FileStore) Stage(ctx context.Context, appID, environment, configID, targetProject, expectedEmail string, data []byte) (StageResult, error) {
	if ctx == nil || ctx.Err() != nil || s == nil || !canonicalUUID(appID) || !canonicalUUID(configID) ||
		(environment != "staging" && environment != "production") || len(data) == 0 || len(data) > maxCredentialSize ||
		fcm.ValidateServiceAccount(data, targetProject, expectedEmail) != nil || ctx.Err() != nil {
		return StageResult{}, errFileStore
	}
	if !trustedRoot(s.root) {
		return StageResult{}, errFileStore
	}

	dir := filepath.Join(s.root, appID, environment, configID)
	if err := ensureDirectory(filepath.Dir(filepath.Dir(dir))); err != nil {
		return StageResult{}, errFileStore
	}
	if err := ensureDirectory(filepath.Dir(dir)); err != nil {
		return StageResult{}, errFileStore
	}
	if err := ensureDirectory(dir); err != nil {
		return StageResult{}, errFileStore
	}
	if ctx.Err() != nil {
		return StageResult{}, errFileStore
	}
	result := StageResult{
		Reference: configID,
		AppID:     appID, Environment: environment, ConfigID: configID, Size: int64(len(data)),
	}
	path := filepath.Join(dir, "service-account.json")
	if existing, err := readExisting(path); err == nil {
		if bytes.Equal(existing, data) {
			if syncDirectory(dir) != nil {
				return StageResult{}, errFileStore
			}
			return result, nil
		}
		return StageResult{}, errFileStore
	}
	if ctx.Err() != nil {
		return StageResult{}, errFileStore
	}
	tmp, err := os.CreateTemp(dir, ".stage-*")
	if err != nil {
		return StageResult{}, errFileStore
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err = tmp.Chmod(0640); err == nil {
		err = setCredentialOwner(tmp)
	}
	if err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil || ctx.Err() != nil || !trustedEntry(tmpPath, false) {
		return StageResult{}, errFileStore
	}
	// A hard link publishes atomically and fails if another writer already won.
	if err := os.Link(tmpPath, path); err != nil {
		if existing, readErr := readExisting(path); readErr == nil && bytes.Equal(existing, data) {
			if syncDirectory(dir) != nil {
				return StageResult{}, errFileStore
			}
			return result, nil
		}
		return StageResult{}, errFileStore
	}
	if err := os.Remove(tmpPath); err != nil {
		return StageResult{}, errFileStore
	}
	if err := syncDirectory(dir); err != nil {
		return StageResult{}, errFileStore
	}
	return result, nil
}

func canonicalUUID(value string) bool {
	if len(value) != 36 || value != strings.ToLower(value) {
		return false
	}
	nonzero := false
	for i, b := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if b != '-' {
				return false
			}
			continue
		}
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return false
		}
		if b != '0' && b != '-' {
			nonzero = true
		}
	}
	return nonzero
}

func ensureDirectory(path string) error {
	if !trustedEntry(path, true) {
		if err := os.Mkdir(path, 0750); err != nil {
			if os.IsExist(err) && trustedEntry(path, true) {
				return nil
			}
			return err
		}
		if err := os.Chmod(path, 0750); err != nil {
			return err
		}
		if err := setDirectoryOwner(path); err != nil {
			return err
		}
		if !trustedEntry(path, true) {
			return errFileStore
		}
		if err := syncDirectory(filepath.Dir(path)); err != nil {
			return err
		}
	}
	return nil
}

func readExisting(path string) ([]byte, error) {
	if !trustedEntry(path, false) {
		return nil, errFileStore
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	before, err := os.Lstat(path)
	if err != nil || before.Mode()&os.ModeSymlink != 0 {
		return nil, errFileStore
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || !trustedEntry(path, false) {
		return nil, errFileStore
	}
	data, err := io.ReadAll(io.LimitReader(file, maxCredentialSize+1))
	if err != nil || len(data) > maxCredentialSize {
		return nil, errFileStore
	}
	return data, nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
