// Package executor runs only policy-reviewed invocations, without a shell.
package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"idrcmd/internal/policy"
)

// OutputLimit is the combined stdout/stderr capture limit per subprocess,
// including the private Git config audit. Excess bytes are discarded while
// pipes continue draining, so output cannot block the child.
const OutputLimit = 64 * 1024
const MaxDuration = 10 * time.Second

type Result struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	Truncated bool
}

// Run leaves child stdin disconnected and never consumes the approval input.
// It uses a fresh environment, not the caller's loader, Git, or PATH settings.
// The caller must still re-fetch registry trust and obtain explicit approval.
// Git repositories must not be concurrently modified by an untrusted process:
// the local config audit is a snapshot, not a lock across config audit and status.
func Run(ctx context.Context, invocation policy.Invocation, cwd string) (Result, error) {
	result := Result{ExitCode: -1}
	ctx, cancel := context.WithTimeout(ctx, MaxDuration)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := policy.Revalidate(invocation); err != nil {
		return result, fmt.Errorf("revalidate invocation: %w", err)
	}
	if len(invocation.Args) > 0 && invocation.Args[0] == "--no-optional-locks" {
		// This private fixed-vector parser command does not inspect worktree
		// contents or execute clean/process filters. Request names only so values
		// (which can contain credentials) never enter the capture buffer.
		audit, err := runProcess(ctx, invocation.Executable, []string{"--no-pager", "config", "--null", "--name-only", "--list", "--local", "--no-includes"}, cwd)
		if err != nil {
			return Result{ExitCode: audit.ExitCode, Truncated: audit.Truncated}, fmt.Errorf("audit local Git configuration: %w", err)
		}
		if audit.Truncated {
			return Result{ExitCode: -1, Truncated: true}, errors.New("local Git configuration exceeds audit output limit")
		}
		if audit.Stderr != "" {
			return result, errors.New("local Git configuration audit produced diagnostics")
		}
		if err := validateGitConfig(audit.Stdout); err != nil {
			return result, err
		}
		if err := policy.Revalidate(invocation); err != nil {
			return result, fmt.Errorf("revalidate invocation after Git audit: %w", err)
		}
	}
	return runProcess(ctx, invocation.Executable, invocation.Args, cwd)
}

// runProcess is private: only the reviewed invocation and the fixed config audit
// vector above may reach it. All subprocesses share Run's ten-second deadline.
func runProcess(ctx context.Context, executable string, args []string, cwd string) (Result, error) {
	result := Result{ExitCode: -1}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = cwd
	cmd.Env = []string{
		"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1",
		"GIT_PAGER=cat", "GIT_ATTR_NOSYSTEM=1",
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	// Bound pipe-copy waits if a descendant retains a stream after the child exits.
	cmd.WaitDelay = time.Second
	capture := &boundedCapture{}
	cmd.Stdout = captureWriter{capture: capture}
	cmd.Stderr = captureWriter{capture: capture, stderr: true}
	err := cmd.Run()
	result.Stdout, result.Stderr, result.Truncated = capture.snapshot()
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if ctx.Err() != nil {
		return result, fmt.Errorf("execute reviewed command: %w", ctx.Err())
	}
	if err != nil {
		return result, fmt.Errorf("execute reviewed command: %w", err)
	}
	return result, nil
}

// validateGitConfig parses Git's --null --name-only --list format: each full
// key followed by NUL, with no values. Reject controls and non-ASCII throughout
// the full key, including subsection names. This is a narrow configuration
// gate, not a comprehensive sandbox for arbitrary hostile Git repositories.
func validateGitConfig(output string) error {
	if output == "" {
		return nil
	}
	if !strings.HasSuffix(output, "\x00") {
		return errors.New("malformed local Git configuration audit")
	}
	for key := range strings.SplitSeq(strings.TrimSuffix(output, "\x00"), "\x00") {
		if key == "" || !strings.Contains(key, ".") {
			return errors.New("malformed local Git configuration audit")
		}
		for _, r := range key {
			if r < 0x20 || r > 0x7e {
				return errors.New("unsupported local Git configuration key")
			}
		}
		key = strings.ToLower(key)
		if strings.HasPrefix(key, "filter.") || strings.HasPrefix(key, "diff.") ||
			key == "include.path" || strings.HasPrefix(key, "includeif.") || key == "extensions.worktreeconfig" ||
			key == "extensions.partialclone" || key == "core.sshcommand" || key == "ssh.variant" || strings.HasPrefix(key, "protocol.") ||
			(strings.HasPrefix(key, "remote.") && (strings.HasSuffix(key, ".promisor") || strings.HasSuffix(key, ".partialclonefilter"))) {
			return errors.New("local Git configuration contains an unsupported execution or include setting")
		}
	}
	return nil
}

type boundedCapture struct {
	mu        sync.Mutex
	stdout    bytes.Buffer
	stderr    bytes.Buffer
	size      int
	truncated bool
}

type captureWriter struct {
	capture *boundedCapture
	stderr  bool
}

func (w captureWriter) Write(p []byte) (int, error) {
	c := w.capture
	c.mu.Lock()
	defer c.mu.Unlock()
	keep := min(len(p), OutputLimit-c.size)
	if keep < len(p) {
		c.truncated = true
	}
	if w.stderr {
		c.stderr.Write(p[:keep])
	} else {
		c.stdout.Write(p[:keep])
	}
	c.size += keep
	return len(p), nil
}

func (c *boundedCapture) snapshot() (string, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stdout.String(), c.stderr.String(), c.truncated
}
