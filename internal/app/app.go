// Package app implements the first fixed-argv, read-only user workflow.
// The model proposes a shortlisted name. It never supplies executable arguments.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"idrcmd/internal/executor"
	"idrcmd/internal/policy"
	"idrcmd/internal/registry"
)

type Model interface {
	Embed(context.Context, string) ([]float32, time.Duration, error)
	Complete(context.Context, string, string) (string, time.Duration, error)
}

type Repository interface {
	Search(context.Context, []float32, string, int) ([]registry.Candidate, error)
	Get(context.Context, string) (registry.Tool, error)
}

type Runner func(context.Context, policy.Invocation, string) (executor.Result, error)

type Service struct {
	Model      Model
	ModelID    string
	Repository Repository
	Runner     Runner
}

type Plan struct {
	Tool       registry.Tool
	Invocation policy.Invocation
	Candidates []registry.Candidate
}

// Prepare retrieves only trusted/enabled tools and accepts one argument-free
// model proposal. It does not execute or grant user approval.
func (s Service) Prepare(ctx context.Context, request string) (Plan, error) {
	if err := policy.ValidateRequest(request); err != nil {
		return Plan{}, err
	}
	vector, _, err := s.Model.Embed(ctx, request)
	if err != nil {
		return Plan{}, fmt.Errorf("embed request: %w", err)
	}
	candidates, err := s.Repository.Search(ctx, vector, s.ModelID, 5)
	if err != nil {
		return Plan{}, fmt.Errorf("retrieve commands: %w", err)
	}
	if len(candidates) == 0 {
		return Plan{}, errors.New("no trusted, enabled tools are registered; use tools add first")
	}
	schema, err := toolSchemas(candidates)
	if err != nil {
		return Plan{}, err
	}
	raw, _, err := s.Model.Complete(ctx, request, schema)
	if err != nil {
		return Plan{}, fmt.Errorf("select tool: %w", err)
	}
	name, err := selectedName(raw)
	if err != nil {
		return Plan{}, err
	}
	for _, candidate := range candidates {
		if candidate.Tool.Name != name {
			continue
		}
		if !candidate.Tool.Trusted || !candidate.Tool.Enabled {
			return Plan{}, errors.New("selected tool is disabled or untrusted")
		}
		invocation, err := policy.Prepare(candidate.Tool.Command)
		if err != nil {
			return Plan{}, err
		}
		return Plan{Tool: candidate.Tool, Invocation: invocation, Candidates: candidates}, nil
	}
	return Plan{}, errors.New("model selected a tool outside the trusted shortlist")
}

// Execute must be called only after explicit confirmation of this Plan. Refresh
// trust and policy immediately before invoking the external process boundary.
func (s Service) Execute(ctx context.Context, plan Plan, cwd string) (executor.Result, error) {
	if err := ctx.Err(); err != nil {
		return executor.Result{}, err
	}
	current, err := s.Repository.Get(ctx, plan.Tool.Name)
	if err != nil {
		return executor.Result{}, err
	}
	if !current.Trusted || !current.Enabled || current.ContentHash != plan.Tool.ContentHash || !reflect.DeepEqual(current.Definition, plan.Tool.Definition) {
		return executor.Result{}, errors.New("tool changed, was disabled, or lost trust after preview; request a new preview")
	}
	invocation, err := policy.Prepare(current.Command)
	if err != nil {
		return executor.Result{}, err
	}
	if !reflect.DeepEqual(invocation, plan.Invocation) {
		return executor.Result{}, errors.New("executable or argv changed after preview; request a new preview")
	}
	if s.Runner == nil {
		return executor.Result{}, errors.New("command runner is unavailable")
	}
	return s.Runner(ctx, invocation, cwd)
}

func toolSchemas(candidates []registry.Candidate) (string, error) {
	type parameters struct {
		Type       string         `json:"type"`
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	type schema struct {
		Name        string     `json:"name"`
		Description string     `json:"description"`
		Parameters  parameters `json:"parameters"`
	}
	tools := make([]schema, 0, len(candidates))
	for _, candidate := range candidates {
		if !candidate.Tool.Trusted || !candidate.Tool.Enabled {
			return "", errors.New("repository returned an ineligible command")
		}
		tools = append(tools, schema{candidate.Tool.Name, candidate.Tool.Description, parameters{"object", map[string]any{}, []string{}}})
	}
	data, err := json.Marshal(tools)
	return string(data), err
}

func selectedName(raw string) (string, error) {
	if len(raw) > 16384 {
		return "", errors.New("model output exceeds the allowed size")
	}
	var response struct {
		Type      string          `json:"type"`
		Success   bool            `json:"success"`
		Error     json.RawMessage `json:"error"`
		ErrorCode json.RawMessage `json:"error_code"`
		Calls     []struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"function_calls"`
		Suppressed []json.RawMessage `json:"suppressed_calls"`
		Validation struct {
			Ungrounded []json.RawMessage `json:"ungrounded"`
			Negation   bool              `json:"negation"`
		} `json:"validation"`
	}
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		return "", errors.New("model returned malformed JSON; nothing was executed")
	}
	if response.Type != "call" || !response.Success || strings.TrimSpace(string(response.Error)) != "null" || strings.TrimSpace(string(response.ErrorCode)) != "null" {
		return "", errors.New("model did not return a successful tool proposal; nothing was executed")
	}
	if len(response.Calls) != 1 || len(response.Suppressed) > 0 || len(response.Validation.Ungrounded) > 0 || response.Validation.Negation {
		return "", errors.New("no single supported action was selected; clarify the request or inspect tools list")
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(response.Calls[0].Arguments, &args); err != nil || args == nil || len(args) != 0 {
		return "", errors.New("model proposed arguments, but this slice accepts only fixed argument-free capabilities")
	}
	return response.Calls[0].Name, nil
}
