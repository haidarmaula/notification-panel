package requeue

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The verifier policy is installed by deployment automation. There is no argv
// or environment override for the file that chooses trusted verification keys.
const deploymentConfigurationPath = "/etc/hello/requeue.json"

// LoadDeploymentConfiguration validates the root-owned file and every ancestor
// before loading secrets. Files must be regular, non-executable and private to
// their owner/optional read-only group. Ancestors must be root-owned directories
// without group/world write permission; symlinks are rejected throughout.
func LoadDeploymentConfiguration() (*Configuration, error) {
	path := deploymentConfigurationPath
	if runtime.GOOS == "darwin" {
		path = "/private" + path
	} // /etc is a symlink on macOS.
	return loadDeploymentFile(path)
}

func loadDeploymentFile(path string) (cfg *Configuration, err error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrConfiguration
	}
	current := string(os.PathSeparator)
	info, err := os.Lstat(current)
	if err != nil || !trustedDeploymentEntry(info, true) || !noExtendedACL(current) {
		return nil, ErrConfiguration
	}
	parts := strings.Split(strings.TrimPrefix(path, current), string(os.PathSeparator))
	var expected os.FileInfo
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || !trustedDeploymentEntry(info, i < len(parts)-1) || !noExtendedACL(current) {
			return nil, ErrConfiguration
		}
		expected = info
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrConfiguration
	}
	defer func() {
		if file.Close() != nil {
			cfg = nil
			err = ErrConfiguration
		}
	}()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(expected, actual) || !trustedDeploymentEntry(actual, false) || !noExtendedACL(path) {
		return nil, ErrConfiguration
	}
	cfg, err = LoadConfiguration(file)
	if err != nil {
		return nil, err
	}
	// The main file cannot delegate DB TLS trust to operator-writable files.
	uri, err := url.Parse(cfg.database.ConnString())
	if err != nil {
		return nil, ErrConfiguration
	}
	for _, name := range []string{"sslrootcert", "sslcert", "sslkey"} {
		if path := uri.Query().Get(name); path != "" {
			if !trustedAuxiliaryPath(path, name == "sslkey") {
				return nil, ErrConfiguration
			}
		}
	}
	return cfg, nil
}

func trustedDeploymentEntry(info os.FileInfo, directory bool) bool {
	if info == nil || !rootOwned(info) {
		return false
	}
	if directory {
		return info.IsDir() && info.Mode().Perm()&0022 == 0
	}
	return info.Mode().IsRegular() && info.Mode().Perm()&0137 == 0 && info.Size() > 0 && info.Size() <= 64*1024
}

func trustedAuxiliaryPath(path string, private bool) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	current := string(os.PathSeparator)
	for _, part := range strings.Split(strings.TrimPrefix(path, current), string(os.PathSeparator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || !rootOwned(info) || !noExtendedACL(current) {
			return false
		}
		if current != path {
			if !trustedDeploymentEntry(info, true) {
				return false
			}
			continue
		}
		forbidden := os.FileMode(0137)
		if !private {
			forbidden = 0133
		}
		return info.Mode().IsRegular() && info.Mode().Perm()&forbidden == 0 && info.Size() > 0 && info.Size() <= 1024*1024
	}
	return false
}
