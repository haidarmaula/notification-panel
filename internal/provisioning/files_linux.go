//go:build linux

package provisioning

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func trustedRoot(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	current := string(os.PathSeparator)
	if !trustedAncestor(current) {
		return false
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, current), string(os.PathSeparator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !noExtendedACL(current) {
			return false
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 || info.Mode().Perm()&0022 != 0 {
			return false
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Gid == workerGroupID && info.Mode().Perm() == 0750
}

func trustedAncestor(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !noExtendedACL(path) {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0 && info.Mode().Perm()&0022 == 0
}

func trustedEntry(path string, directory bool) bool {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !noExtendedACL(path) {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || stat.Gid != workerGroupID {
		return false
	}
	if directory {
		return info.IsDir() && info.Mode().Perm() == 0750
	}
	return info.Mode().IsRegular() && info.Mode().Perm() == 0640 && info.Size() > 0 && info.Size() <= maxCredentialSize
}

func setDirectoryOwner(path string) error    { return os.Chown(path, 0, workerGroupID) }
func setCredentialOwner(file *os.File) error { return file.Chown(0, workerGroupID) }

func noExtendedACL(path string) bool {
	for _, name := range []string{"system.posix_acl_access", "system.posix_acl_default"} {
		_, err := syscall.Getxattr(path, name, nil)
		if err != syscall.ENODATA {
			return false
		}
	}
	return true
}
