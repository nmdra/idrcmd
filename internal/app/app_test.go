package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"idrcmd/internal/executor"
	"idrcmd/internal/policy"
	"idrcmd/internal/registry"
)

type fakeModel struct {
	raw     string
	schemas string
	calls   int
}

func (m *fakeModel) Embed(context.Context, string) ([]float32, time.Duration, error) {
	return []float32{1, 0}, 0, nil
}
func (m *fakeModel) Complete(_ context.Context, _ string, schema string) (string, time.Duration, error) {
	m.schemas = schema
	m.calls++
	return m.raw, 0, nil
}

type fakeRepository struct {
	tool       registry.Tool
	candidates []registry.Candidate
}

func (r *fakeRepository) Search(context.Context, []float32, string, int) ([]registry.Candidate, error) {
	return r.candidates, nil
}
func (r *fakeRepository) Get(context.Context, string) (registry.Tool, error) { return r.tool, nil }
func proposal(name string, args any) string {
	data, _ := json.Marshal(map[string]any{"type": "call", "success": true, "error": nil, "error_code": nil, "function_calls": []any{map[string]any{"name": name, "arguments": args}}})
	return string(data)
}
func serviceFixture() (Service, *fakeRepository, *fakeModel, *int) {
	tool := registry.Tool{Definition: registry.Definition{Name: "where_am_i", Description: "Show the current working directory", Command: []string{"pwd"}}, ContentHash: "fixed-test-hash", Trusted: true, Enabled: true}
	repo := &fakeRepository{tool: tool, candidates: []registry.Candidate{{Tool: tool, Distance: 0}}}
	model := &fakeModel{raw: proposal(tool.Name, map[string]any{})}
	runs := new(int)
	service := Service{Model: model, ModelID: "fake-plumbing-only", Repository: repo, Runner: func(context.Context, policy.Invocation, string) (executor.Result, error) {
		*runs++
		return executor.Result{Stdout: "fixture", ExitCode: 0}, nil
	}}
	return service, repo, model, runs
}
func TestPrepareDoesNotExecuteAndRefreshesTrust(t *testing.T) {
	s, repo, model, runs := serviceFixture()
	plan, err := s.Prepare(context.Background(), "where am I?")
	if err != nil {
		t.Fatal(err)
	}
	if *runs != 0 || !strings.Contains(model.schemas, "where_am_i") {
		t.Fatal("prepare executed or omitted shortlist")
	}
	repo.tool.Enabled = false
	if _, err := s.Execute(context.Background(), plan, t.TempDir()); err == nil || *runs != 0 {
		t.Fatal("disabled-after-preview tool ran")
	}
	repo.tool.Enabled = true
	if _, err := s.Execute(context.Background(), plan, t.TempDir()); err != nil || *runs != 1 {
		t.Fatalf("approved fixed command did not reach runner: %v", err)
	}
}
func TestPostPreviewChangesBlockExecution(t *testing.T) {
	for _, change := range []string{"trust", "hash", "definition", "binary"} {
		t.Run(change, func(t *testing.T) {
			s, repo, _, runs := serviceFixture()
			plan, err := s.Prepare(context.Background(), "where am I?")
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "trust":
				repo.tool.Trusted = false
			case "hash":
				repo.tool.ContentHash = "changed"
			case "definition":
				repo.tool.Command = []string{"uname", "-a"}
			case "binary":
				plan.Invocation.SHA256 = "wrong"
			}
			if _, err := s.Execute(context.Background(), plan, t.TempDir()); err == nil || *runs != 0 {
				t.Fatal("changed tool reached runner")
			}
		})
	}
}
func TestUntrustedUnknownAndShellRequestsNeverExecute(t *testing.T) {
	for _, kind := range []string{"untrusted", "unknown", "argument", "null-arguments", "empty", "malformed", "shell"} {
		t.Run(kind, func(t *testing.T) {
			s, repo, model, runs := serviceFixture()
			request := "where am I?"
			switch kind {
			case "untrusted":
				repo.candidates[0].Tool.Trusted = false
			case "unknown":
				model.raw = proposal("unregistered", map[string]any{})
			case "argument":
				model.raw = proposal("where_am_i", map[string]any{"flags": "--delete"})
			case "null-arguments":
				model.raw = proposal("where_am_i", nil)
			case "empty":
				model.raw = `{"type":"call","success":true,"error":null,"error_code":null,"function_calls":[]}`
			case "malformed":
				model.raw = "{"
			case "shell":
				request = "where am I; touch PWNED"
			}
			if _, err := s.Prepare(context.Background(), request); err == nil || *runs != 0 {
				t.Fatal("unsafe proposal accepted or executed")
			}
			if kind == "shell" && model.calls != 0 {
				t.Fatal("shell syntax reached inference")
			}
		})
	}
}
func TestCancelledExecutionDoesNotRun(t *testing.T) {
	s, _, _, runs := serviceFixture()
	plan, err := s.Prepare(context.Background(), "where am I?")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Execute(ctx, plan, t.TempDir()); !errors.Is(err, context.Canceled) || *runs != 0 {
		t.Fatal("cancelled execution reached runner")
	}
}
