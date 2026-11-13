package deployment

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"hello/internal/jsonobject"
)

// Read loads a fixed root-owned component policy. No argv/environment path
// override exists. Symlinks, extended ACLs and unsafe ownership/modes fail closed.
func Read(component Component) ([]byte, error) {
	switch component {
	case Requeue, Worker, Scheduler, Server, Bootstrap, Retention, Dashboard:
	default:
		return nil, ErrConfiguration
	}
	path := "/etc/hello/" + string(component) + ".json"
	if runtime.GOOS == "darwin" {
		path = "/private" + path
	}
	return readFile(path)
}

// CheckDatabaseFiles rejects operator-writable DB TLS trust/key files. The
// supplied connection string must already have passed Database validation.
func CheckDatabaseFiles(connection string) error {
	uri, err := url.Parse(connection)
	if err != nil {
		return ErrConfiguration
	}
	for _, name := range []string{"sslrootcert", "sslcert", "sslkey"} {
		if path := uri.Query().Get(name); path != "" && !trustedAuxiliaryPath(path, name == "sslkey") {
			return ErrConfiguration
		}
	}
	return nil
}

// ReadSecret reads bounded JSON from an explicitly trusted deployment secret path.
// The file and every ancestor must pass the same protections as component policy.
// Callers must validate its schema and must not expose the returned bytes.
func ReadSecret(path string) ([]byte, error) {
	return readFile(path)
}

func readFile(path string) (data []byte, err error) {
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
			data = nil
			err = ErrConfiguration
		}
	}()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(expected, actual) || !trustedDeploymentEntry(actual, false) || !noExtendedACL(path) {
		return nil, ErrConfiguration
	}
	value, readErr := jsonobject.Read(file)
	if readErr != nil {
		return nil, ErrConfiguration
	}
	return value, nil
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
