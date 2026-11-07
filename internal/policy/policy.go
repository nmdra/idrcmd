// Package policy admits only a small reviewed set of read-only command templates.
// Registry trust and user approval remain the caller's responsibility.
package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

// Invocation binds a reviewed argument vector to a canonical binary and its hash.
// Callers must not change it after approval. Revalidate checks all three fields.
type Invocation struct {
	Executable string
	Args       []string
	SHA256     string
}

var gitArgs = []string{"--no-optional-locks", "--no-pager", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "core.hooksPath=/dev/null", "-c", "color.ui=false", "-c", "core.quotePath=true", "status", "--short", "--ignore-submodules=all"}

// Prepare rejects all templates except the exact reviewed token patterns. It does
// not search PATH. pwd always uses physical rather than environment-derived paths.
func Prepare(command []string) (Invocation, error) {
	var name string
	var args []string
	switch {
	case reflect.DeepEqual(command, []string{"git", "status", "--short"}):
		name, args = "git", gitArgs
	case reflect.DeepEqual(command, []string{"pwd"}), reflect.DeepEqual(command, []string{"pwd", "-P"}):
		name, args = "pwd", []string{"-P"}
	case reflect.DeepEqual(command, []string{"uname", "-a"}):
		name, args = "uname", []string{"-a"}
	default:
		return Invocation{}, errors.New("command is not a reviewed read-only template")
	}
	path, err := resolve(name)
	if err != nil {
		return Invocation{}, err
	}
	hash, err := binaryHash(path)
	if err != nil {
		return Invocation{}, err
	}
	return Invocation{Executable: path, Args: append([]string(nil), args...), SHA256: hash}, nil
}

// Revalidate checks the binary identity and reviewed arguments immediately before
// spawning. It cannot eliminate the kernel path lookup race if root replaces a
// binary between this check and exec; root and the system installation are trusted.
func Revalidate(inv Invocation) error {
	var command []string
	switch {
	case reflect.DeepEqual(inv.Args, gitArgs):
		command = []string{"git", "status", "--short"}
	case reflect.DeepEqual(inv.Args, []string{"-P"}):
		command = []string{"pwd"}
	case reflect.DeepEqual(inv.Args, []string{"-a"}):
		command = []string{"uname", "-a"}
	default:
		return errors.New("invocation arguments are not reviewed")
	}
	expected, err := Prepare(command)
	if err != nil {
		return err
	}
	if inv.Executable != expected.Executable || inv.SHA256 != expected.SHA256 {
		return errors.New("invocation binary identity changed or is not trusted")
	}
	return nil
}

func resolve(name string) (string, error) {
	for _, dir := range []string{"/usr/bin", "/bin"} {
		path, err := filepath.EvalSymlinks(filepath.Join(dir, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("resolve trusted executable: %w", err)
		}
		// Check the canonical parent chain as well as the binary. An unprivileged
		// writer must not be able to replace a directory entry after validation.
		for p := path; ; p = filepath.Dir(p) {
			info, err := os.Lstat(p)
			if err != nil {
				return "", fmt.Errorf("inspect trusted executable: %w", err)
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok || stat.Uid != 0 || info.Mode().Perm()&0022 != 0 {
				return "", errors.New("executable or parent is not root-owned and write-protected")
			}
			if p == path && (!info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0) {
				return "", errors.New("trusted executable must be a regular executable file")
			}
			if p != path && !info.IsDir() {
				return "", errors.New("executable parent is not a directory")
			}
			if p == "/" {
				break
			}
		}
		return path, nil
	}
	return "", fmt.Errorf("reviewed executable %s is unavailable", name)
}

func binaryHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open trusted executable: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash trusted executable: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ValidateRequest is a conservative input gate, not an intent router. Its byte
// limit bounds model input; execution still requires the reviewed template policy.
func ValidateRequest(request string) error {
	if len(request) > 4096 || strings.TrimSpace(request) == "" || !utf8.ValidString(request) {
		return errors.New("request must be nonempty valid UTF-8 of at most 4096 bytes")
	}
	for _, r := range request {
		if unicode.IsControl(r) {
			return errors.New("request contains a control character")
		}
	}
	if strings.ContainsAny(request, "|;&><`") || strings.Contains(request, "$(") {
		return errors.New("request contains a shell operator")
	}
	return nil
}
