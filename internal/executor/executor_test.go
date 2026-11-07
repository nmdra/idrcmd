package executor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"idrcmd/internal/policy"
)

func prepared(t *testing.T, command ...string) policy.Invocation {
	t.Helper()
	inv, err := policy.Prepare(command)
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func TestRunReviewedCommands(t *testing.T) {
	dir := t.TempDir()
	inv := prepared(t, "pwd")
	result, err := Run(context.Background(), inv, dir)
	if err != nil {
		t.Fatal(err)
	}
	physical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != physical+"\n" || result.Stderr != "" || result.ExitCode != 0 || result.Truncated {
		t.Fatalf("result: %+v", result)
	}
	result, err = Run(context.Background(), prepared(t, "uname", "-a"), dir)
	if err != nil || !strings.Contains(result.Stdout, "Linux") {
		t.Fatalf("uname: %+v, %v", result, err)
	}
}

func TestRejectForgedInvocation(t *testing.T) {
	inv := prepared(t, "pwd")
	inv.SHA256 = "changed"
	if _, err := Run(context.Background(), inv, t.TempDir()); err == nil {
		t.Fatal("accepted forged identity")
	}
	inv = prepared(t, "pwd")
	inv.Args = []string{"--help"}
	if _, err := Run(context.Background(), inv, t.TempDir()); err == nil {
		t.Fatal("accepted forged args")
	}
}

func TestGitHardeningAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "EXECUTED")
	script := filepath.Join(dir, "malicious")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	git := prepared(t, "git", "status", "--short")
	setup := func(args ...string) {
		t.Helper()
		cmd := exec.Command(git.Executable, args...)
		cmd.Dir = dir
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git setup %q: %v %s", args, err, out)
		}
	}
	setup("init", "--quiet")
	setup("config", "core.fsmonitor", script)
	setup("config", "core.untrackedCache", "true")
	setup("config", "color.ui", "always")
	setup("config", "core.quotePath", "false")
	if err := os.WriteFile(filepath.Join(dir, "file\nname"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	// No setting from the caller may select an executable, config, or loader.
	t.Setenv("PATH", dir)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.fsmonitor")
	t.Setenv("GIT_CONFIG_VALUE_0", script)
	t.Setenv("GIT_EXEC_PATH", dir)
	t.Setenv("LD_PRELOAD", "/nonexistent/idrcmd-preload.so")
	t.Setenv("GIT_PAGER", script)
	t.Setenv("GIT_CONFIG_GLOBAL", script)
	result, err := Run(context.Background(), git, dir)
	if err != nil {
		t.Fatalf("git: %v: %+v", err, result)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("malicious script ran or marker error: %v", err)
	}
	if strings.Contains(result.Stdout, "\x1b") || !strings.Contains(result.Stdout, `"file\nname"`) || result.Stderr != "" {
		t.Fatalf("unsafe output or environment: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "index")); !os.IsNotExist(err) {
		t.Fatalf("status wrote an index: %v", err)
	}
	// The same malicious PATH must not affect non-Git commands either.
	if err := os.WriteFile(filepath.Join(dir, "pwd"), []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), prepared(t, "pwd"), dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("PATH executable ran")
	}
}

func TestExitFailure(t *testing.T) {
	result, err := Run(context.Background(), prepared(t, "git", "status", "--short"), t.TempDir())
	var exit *exec.ExitError
	if !errors.As(err, &exit) || result.ExitCode == 0 {
		t.Fatalf("missing exit diagnostics: %+v %v", result, err)
	}
}

func TestCancelledAndExpiredContext(t *testing.T) {
	inv := prepared(t, "pwd")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, inv, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, err := Run(ctx, inv, t.TempDir()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
}

func TestCombinedOutputBound(t *testing.T) {
	capture := &boundedCapture{}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(stderr bool) {
			defer wg.Done()
			data := []byte(strings.Repeat("x", OutputLimit))
			n, err := (captureWriter{capture: capture, stderr: stderr}).Write(data)
			if n != len(data) || err != nil {
				t.Errorf("write: %d, %v", n, err)
			}
		}(i%2 == 0)
	}
	wg.Wait()
	stdout, stderr, truncated := capture.snapshot()
	if len(stdout)+len(stderr) != OutputLimit || !truncated {
		t.Fatalf("capture limit: %d %d %v", len(stdout), len(stderr), truncated)
	}
}
