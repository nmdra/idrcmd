package executor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"idrcmd/internal/policy"
)

func gitFixture(t *testing.T) (string, policy.Invocation, func(...string) string) {
	t.Helper()
	dir := t.TempDir()
	inv := prepared(t, "git", "status", "--short")
	setup := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(inv.Executable, args...)
		cmd.Dir = dir
		cmd.Env = []string{"PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0"}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git fixture %q: %v %q", args, err, out)
		}
		return string(out)
	}
	setup("init", "--quiet")
	return dir, inv, setup
}

func TestGitConfigNullFormat(t *testing.T) {
	_, _, setup := gitFixture(t)
	setup("config", "audit.value", "first\nsecond")
	setup("config", "audit.empty", "")
	raw := setup("--no-pager", "config", "--null", "--name-only", "--list", "--local", "--no-includes")
	if !strings.Contains(raw, "audit.value\x00") || !strings.Contains(raw, "audit.empty\x00") || strings.Contains(raw, "first") || strings.Contains(raw, "\n") {
		t.Fatalf("unexpected system Git -z config format: %q", raw)
	}
	if err := validateGitConfig(raw); err != nil {
		t.Fatal(err)
	}
	t.Log(strings.TrimSpace(setup("--version")))
}

func TestGitFilterRefusedBeforeStatus(t *testing.T) {
	for _, kind := range []string{"clean", "process"} {
		t.Run(kind, func(t *testing.T) {
			dir, inv, setup := gitFixture(t)
			marker := filepath.Join(dir, "EXECUTED")
			script := filepath.Join(dir, "filter-script")
			if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("tracked filter=malicious\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "tracked"), []byte("before\n"), 0600); err != nil {
				t.Fatal(err)
			}
			// Populate the index before installing the filter. Setup never runs it.
			setup("add", "--", ".gitattributes", "tracked")
			setup("config", "filter.malicious."+kind, script)
			if err := os.WriteFile(filepath.Join(dir, "tracked"), []byte("changed content\n"), 0600); err != nil {
				t.Fatal(err)
			}
			result, err := Run(context.Background(), inv, dir)
			if err == nil || !strings.Contains(err.Error(), "unsupported execution or include setting") {
				t.Fatalf("filter not refused: %+v %v", result, err)
			}
			if result.Stdout != "" || result.Stderr != "" {
				t.Fatalf("audit leaked config: %+v", result)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("filter executed or marker stat failed: %v", err)
			}
		})
	}
}

func TestGitUnsafeLocalConfigRefused(t *testing.T) {
	for _, key := range []string{"include.path", "includeIf.gitdir:/**.path", "extensions.worktreeConfig", "diff.external", "diff.custom.textconv", "extensions.partialclone", "remote.origin.promisor", "remote.origin.partialclonefilter", "core.sshCommand", "ssh.variant", "protocol.ext.allow"} {
		t.Run(key, func(t *testing.T) {
			dir, inv, setup := gitFixture(t)
			outside := filepath.Join(t.TempDir(), "outside-config")
			if err := os.WriteFile(outside, []byte("[filter \"external\"]\nclean = false\n"), 0600); err != nil {
				t.Fatal(err)
			}
			value := outside
			if key == "extensions.worktreeConfig" {
				value = "true"
			}
			setup("config", key, value)
			result, err := Run(context.Background(), inv, dir)
			if err == nil || !strings.Contains(err.Error(), "unsupported execution or include setting") {
				t.Fatalf("setting not refused: %+v %v", result, err)
			}
			if result.Stdout != "" || result.Stderr != "" {
				t.Fatal("audit output exposed")
			}
		})
	}
}

func TestGitAuditMalformedAndOversize(t *testing.T) {
	t.Run("malformed-config", func(t *testing.T) {
		dir, inv, _ := gitFixture(t)
		if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("[invalid\n"), 0600); err != nil {
			t.Fatal(err)
		}
		result, err := Run(context.Background(), inv, dir)
		if err == nil || result.ExitCode == 0 || result.Stdout != "" || result.Stderr != "" {
			t.Fatalf("malformed audit not refused: %+v %v", result, err)
		}
	})
	t.Run("oversize-config", func(t *testing.T) {
		dir, inv, setup := gitFixture(t)
		setup("config", "audit."+strings.Repeat("x", OutputLimit)+".large", "value-not-captured")
		result, err := Run(context.Background(), inv, dir)
		if err == nil || !result.Truncated || result.Stdout != "" || result.Stderr != "" {
			t.Fatalf("oversize audit not refused: %+v %v", result, err)
		}
	})
}

func TestValidateGitConfig(t *testing.T) {
	for _, input := range []string{"", "core.bare\x00", "audit.value\x00audit.empty\x00", "remote.origin.url\x00remote.origin.fetch\x00"} {
		if err := validateGitConfig(input); err != nil {
			t.Errorf("valid %q: %v", input, err)
		}
	}
	for _, input := range []string{"missing-terminator", "key\x00", "\x00", "\nvalue\x00", "nodot\x00", "core.bad\tkey\x00", "filter.any.clean\x00", "filter.any.process\x00", "diff.external\x00", "diff.name.textconv\x00", "diff.name\n.textconv\x00", "include.path\x00", "includeif.gitdir:test.path\x00", "extensions.worktreeconfig\x00", "FILTER.Test.clean\x00", "extensions.partialclone\x00", "remote.origin.promisor\x00", "remote.origin.partialclonefilter\x00", "remote.multi.part.promisor\x00", "core.sshcommand\x00", "ssh.variant\x00", "protocol.ext.allow\x00", "remote.nonasciié.url\x00", "remote.bad\rname.url\x00", "core.bare\x00\x00"} {
		if validateGitConfig(input) == nil {
			t.Errorf("accepted %q", input)
		}
	}
}

func TestGitOrdinaryRemoteAllowed(t *testing.T) {
	dir, inv, setup := gitFixture(t)
	// Merely storing a remote is allowed. Status must not contact it.
	setup("config", "remote.origin.url", "https://example.invalid/not-contacted")
	setup("config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	if result, err := Run(context.Background(), inv, dir); err != nil {
		t.Fatalf("ordinary remote refused: %+v %v", result, err)
	}
}

func TestGitPartialCloneMissingCRLFBlobRefused(t *testing.T) {
	dir, inv, setup := gitFixture(t)
	marker := filepath.Join(dir, "HELPER_EXECUTED")
	helper := filepath.Join(dir, "remote-helper")
	// This sentinel has no networking behavior, even if a regression invokes it.
	if err := os.WriteFile(helper, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	setup("config", "core.autocrlf", "true")
	if err := os.WriteFile(filepath.Join(dir, "tracked"), []byte("before\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	setup("add", "--", "tracked")
	oid := strings.TrimSpace(setup("rev-parse", ":tracked"))
	if len(oid) != 40 {
		t.Fatalf("unexpected fixture object ID %q", oid)
	}
	// Remove only a newly created object inside this temporary repository.
	if err := os.Remove(filepath.Join(dir, ".git", "objects", oid[:2], oid[2:])); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tracked"), []byte("changed\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	setup("config", "core.repositoryformatversion", "1")
	setup("config", "extensions.partialclone", "origin")
	setup("config", "remote.origin.url", "ext::"+helper)
	setup("config", "remote.origin.promisor", "true")
	setup("config", "remote.origin.partialclonefilter", "blob:none")
	setup("config", "protocol.ext.allow", "always")
	result, err := Run(context.Background(), inv, dir)
	if err == nil || !strings.Contains(err.Error(), "unsupported execution or include setting") {
		t.Fatalf("partial clone not refused: %+v %v", result, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("helper executed or stat failed: %v", err)
	}
}

func TestGitControlSubsectionRefused(t *testing.T) {
	for _, subsection := range []string{"line\rbreak", "nonasciié"} {
		t.Run(subsection, func(t *testing.T) {
			dir, inv, setup := gitFixture(t)
			setup("config", "remote."+subsection+".url", "not-contacted")
			if result, err := Run(context.Background(), inv, dir); err == nil {
				t.Fatalf("unsafe key accepted: %+v", result)
			}
		})
	}
}
