package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Paths must remain under caller control while the store is open. Each existing
// ancestor must be owned by root or the current user and must not be writable
// by group/others, except root-owned sticky directories such as /tmp. Lstat
// checks cannot prevent concurrent replacement by the same user or root.
func preparePath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || strings.ContainsAny(path, ":?#\x00\r\n") {
		return errors.New("database requires a clean absolute local filename without DSN syntax")
	}
	var missing []string
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			missing = append(missing, dir)
		} else if err != nil {
			return err
		} else {
			if err := validateAncestor(info); err != nil {
				return fmt.Errorf("inspect database ancestor %s: %w", dir, err)
			}
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], 0700); err != nil {
			return fmt.Errorf("create private database parent: %w", err)
		}
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		info, err := os.Lstat(path + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return errors.New("database and sidecars must be private regular files without symlinks")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
			return errors.New("database and sidecars must be owned by current user without hard links")
		}
	}
	// The pinned driver creates its WAL with 0644 by default. Reserve both
	// files privately before native code opens them. Empty WALs are supported.
	for _, suffix := range []string{"", "-wal"} {
		if _, err := os.Lstat(path + suffix); errors.Is(err, os.ErrNotExist) {
			f, err := os.OpenFile(path+suffix, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	return nil
}

func validateAncestor(info os.FileInfo) error {
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("database ancestors must be directories without symlinks")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || (stat.Uid != 0 && stat.Uid != uint32(os.Geteuid())) {
		return errors.New("database ancestors must be owned by root or current user")
	}
	trustedSticky := stat.Uid == 0 && info.Mode()&os.ModeSticky != 0
	if info.Mode().Perm()&0022 != 0 && !trustedSticky {
		return errors.New("database ancestors must not be writable by group or others unless root-owned and sticky")
	}
	return nil
}
