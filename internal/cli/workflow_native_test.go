//go:build needle && cgo && linux && amd64

package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"idrcmd/internal/registry"
)

type readFunc func([]byte) (int, error)

func (f readFunc) Read(p []byte) (int, error) { return f(p) }

// This exercises real model retrieval and actual read-only Git status through
// the CLI boundary. It never runs a modifying user command or a model shell.
func TestNativeRegistrationExecution(t *testing.T) {
	model := os.Getenv("IDRCMD_NEEDLE_MODEL")
	if model == "" {
		t.Skip("set IDRCMD_NEEDLE_MODEL for real registration/execution")
	}
	database, _ := isolatedPaths(t)
	t.Setenv("IDRCMD_MODEL", model)
	repo := t.TempDir()
	cmd := exec.Command("/usr/bin/git", "init", repo)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init fixture: %v %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(repo, "user-note.txt"), []byte("public smoke fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	definition := filepath.Join(t.TempDir(), "git-status.json")
	if err := os.WriteFile(definition, []byte(`{"name":"git_status","description":"Show uncommitted changes and working tree status in the current Git repository, read only","command":["git","status","--short"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args []string, in io.Reader) (int, string, string) {
		var out, errs bytes.Buffer
		code := RunWithInput(context.Background(), args, in, &out, &errs)
		return code, out.String(), errs.String()
	}
	code, out, errs := run([]string{"tools", "add", definition}, strings.NewReader("yes\n"))
	if code != 0 || !strings.Contains(out, "Registered and indexed") {
		t.Fatalf("registration exit=%d out=%s err=%s", code, out, errs)
	}
	code, out, errs = run([]string{"tools", "list"}, strings.NewReader(""))
	if code != 0 || !strings.Contains(out, "git_status  trusted=true enabled=true") {
		t.Fatalf("persistent list: %d %s %s", code, out, errs)
	}
	code, out, errs = run([]string{"--dry-run", "show my uncommitted changes"}, strings.NewReader("yes\n"))
	if code != 0 || !strings.Contains(out, "Dry run: nothing was executed") || strings.Contains(out, "?? user-note.txt") || strings.Contains(out, "Execute this") {
		t.Fatalf("dry run: %d %s %s", code, out, errs)
	}
	code, out, errs = run([]string{"show my uncommitted changes"}, strings.NewReader("n\n"))
	if code != 0 || !strings.Contains(out, "Cancelled. Nothing was executed") || strings.Contains(out, "?? user-note.txt") {
		t.Fatalf("decline: %d %s %s", code, out, errs)
	}
	code, out, errs = run([]string{"--explain", "show my uncommitted changes"}, strings.NewReader("yes\n"))
	if code != 0 || !strings.Contains(out, "?? user-note.txt") || !strings.Contains(out, "Completed.") {
		t.Fatalf("real approved execution: %d %s %s", code, out, errs)
	}
	code, out, errs = run([]string{"book me a flight to Paris"}, strings.NewReader("yes\n"))
	if code == 0 || strings.Contains(out, "Execute this") {
		t.Fatalf("unsupported request was actionable: %d %s %s", code, out, errs)
	}
	mutated := false
	reader := readFunc(func(p []byte) (int, error) {
		if mutated {
			return 0, io.EOF
		}
		mutated = true
		store, err := registry.Open(context.Background(), database)
		if err != nil {
			return 0, err
		}
		err = store.Disable(context.Background(), "git_status")
		closeErr := store.Close()
		if err != nil {
			return 0, err
		}
		if closeErr != nil {
			return 0, closeErr
		}
		return copy(p, "yes\n"), nil
	})
	code, out, errs = run([]string{"show my uncommitted changes"}, reader)
	if !mutated || code == 0 || strings.Contains(out, "?? user-note.txt") || !strings.Contains(errs, "disabled") {
		t.Fatalf("disable-after-preview: %d %s %s", code, out, errs)
	}
	code, out, errs = run([]string{"show my uncommitted changes"}, strings.NewReader("yes\n"))
	if code == 0 || strings.Contains(out, "Execute this") {
		t.Fatalf("disabled tool remained actionable: %d %s %s", code, out, errs)
	}
	custom := filepath.Join(t.TempDir(), "custom.json")
	if err := os.WriteFile(custom, []byte(`{"name":"my_location","description":"Show the current working directory","command":["pwd"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	code, out, errs = run([]string{"tools", "add", custom}, strings.NewReader("yes\n"))
	if code != 0 {
		t.Fatalf("custom-name registration: %d %s %s", code, out, errs)
	}
	code, out, errs = run([]string{"what directory am I in?"}, strings.NewReader("yes\n"))
	if code != 0 || !strings.Contains(out, `Tool: "my_location"`) || !strings.Contains(out, repo+"\nCompleted.") {
		t.Fatalf("new name without rebuilding: %d %s %s", code, out, errs)
	}
	t.Log("real CLI flow verified: registration, persistence, dry-run, decline, approved Git status, no-match, disable after preview, disabled retrieval, and custom-name pwd without rebuilding")
}
