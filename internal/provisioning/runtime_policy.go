package provisioning

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"unicode/utf8"

	"hello/internal/deployment"
)

const deliveryPolicyRoot = "/var/lib/hello/runtime"
const maxDeliveryPolicySize = 64 * 1024

var errDeliveryPolicy = errors.New("delivery policy publication failed")

// DeliveryPolicyPath derives the fixed host location of one delivery generation.
// A valid path is independent of whether publication has occurred.
func DeliveryPolicyPath(appID, environment, configID string, generation int64) (string, error) {
	relative, err := deliveryPolicyRelativePath(appID, environment, configID, generation)
	if err != nil {
		return "", err
	}
	return filepath.Join(deliveryPolicyRoot, relative), nil
}

func deliveryPolicyRelativePath(appID, environment, configID string, generation int64) (string, error) {
	if !canonicalUUID(appID) || !canonicalUUID(configID) || generation <= 0 ||
		(environment != "staging" && environment != "production") {
		return "", errDeliveryPolicy
	}
	return filepath.Join(appID, environment, configID, strconv.FormatInt(generation, 10), "worker.json"), nil
}

// PublishDeliveryPolicy writes exact, previously validated deployment policy bytes.
// It requires the root-owned Linux policy store and returns a host path for a
// read-only file mount. Neither publication nor exact replay establishes readiness.
func PublishDeliveryPolicy(ctx context.Context, appID, environment, configID string, generation int64, data []byte) (string, error) {
	return publishDeliveryPolicy(ctx, deliveryPolicyRoot, appID, environment, configID, generation, data)
}

func publishDeliveryPolicy(ctx context.Context, root, appID, environment, configID string, generation int64, data []byte) (string, error) {
	relative, err := deliveryPolicyRelativePath(appID, environment, configID, generation)
	if err != nil {
		return "", err
	}
	if ctx == nil || ctx.Err() != nil || len(data) == 0 || len(data) > maxDeliveryPolicySize ||
		!utf8.Valid(data) || !trustedRoot(root) || os.Geteuid() != 0 {
		return "", errDeliveryPolicy
	}
	path := filepath.Join(root, relative)
	dir := filepath.Dir(path)
	config := filepath.Dir(dir)
	environmentDir := filepath.Dir(config)
	for _, child := range []string{filepath.Dir(environmentDir), environmentDir, config, dir} {
		// Sync every parent on replay as well as creation. Another process may
		// have created a directory but failed before making it durable.
		if ctx.Err() != nil || ensureDirectory(child) != nil || syncDirectory(filepath.Dir(child)) != nil {
			return "", errDeliveryPolicy
		}
	}
	if _, err := os.Lstat(path); err == nil {
		return replayDeliveryPolicy(path, data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", errDeliveryPolicy
	}

	tmp, err := os.CreateTemp(dir, ".worker-policy-*")
	if err != nil {
		return "", errDeliveryPolicy
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	// Write while the temporary inode still has its private creation mode.
	if _, err = tmp.Write(data); err == nil {
		err = setCredentialOwner(tmp)
	}
	if err == nil {
		err = tmp.Chmod(0640)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil || ctx.Err() != nil || !trustedEntry(tmpPath, false) {
		return "", errDeliveryPolicy
	}
	// Linking the synced inode provides an atomic publication without replacing
	// another writer's policy. Existing bytes are accepted only as exact replay.
	if err := os.Link(tmpPath, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return replayDeliveryPolicy(path, data)
		}
		return "", errDeliveryPolicy
	}
	if err := os.Remove(tmpPath); err != nil {
		return "", errDeliveryPolicy
	}
	if err := syncDirectory(dir); err != nil {
		return "", errDeliveryPolicy
	}
	return path, nil
}

func replayDeliveryPolicy(path string, data []byte) (string, error) {
	if !trustedEntry(path, false) {
		return "", errDeliveryPolicy
	}
	existing, err := deployment.ReadSecret(path)
	if err != nil || !bytes.Equal(existing, data) || syncDirectory(filepath.Dir(path)) != nil {
		return "", errDeliveryPolicy
	}
	return path, nil
}
