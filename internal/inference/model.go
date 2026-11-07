package inference

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	maxInputBytes = 16 * 1024
	maxToolsBytes = 64 * 1024
)

// LoadMetrics excludes time spent waiting for the process-global native gate.
// VerifyDuration measures pinned-file verification on every Open call.
// NativeLoadDuration is zero when weights were already loaded.
type LoadMetrics struct {
	VerifyDuration     time.Duration
	NativeLoadDuration time.Duration
}

// Model is an immutable handle to process-lifetime pinned weights. It owns no
// native memory and needs no Close. All operations, including Probe, serialize.
// The ABI cannot interrupt C calls; cancellation is checked around each call.
type Model struct{ ready bool }

// Open verifies the pinned model even if the process already loaded its weights.
func Open(ctx context.Context, modelPath string) (*Model, LoadMetrics, error) {
	if err := ctx.Err(); err != nil {
		return nil, LoadMetrics{}, err
	}
	return openModel(ctx, modelPath)
}

// Embed embeds raw text with an empty tool schema and no conversation history.
// Duration includes initialization and inference, but excludes gate wait time.
func (m *Model) Embed(ctx context.Context, text string) ([]float32, time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if err := validateInput(text); err != nil {
		return nil, 0, err
	}
	return m.embed(ctx, text)
}

// Complete returns bounded raw model output, not execution authority. Empty tool
// calls are allowed. Each call starts a new conversation with the supplied schema.
// Duration includes initialization and inference, but excludes gate wait time.
func (m *Model) Complete(ctx context.Context, request, toolsJSON string) (string, time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	if err := validateInput(request); err != nil {
		return "", 0, err
	}
	if err := validateTools(toolsJSON); err != nil {
		return "", 0, err
	}
	return m.complete(ctx, request, toolsJSON)
}

func validateInput(s string) error {
	if len(s) > maxInputBytes || strings.TrimSpace(s) == "" || strings.ContainsRune(s, 0) {
		return errors.New("Needle input must be nonempty, contain no NUL, and be at most 16384 bytes")
	}
	return nil
}

func validateTools(s string) error {
	if len(s) > maxToolsBytes || strings.ContainsRune(s, 0) {
		return errors.New("Needle tools must contain no NUL and be at most 65536 bytes")
	}
	var tools []json.RawMessage
	if err := json.Unmarshal([]byte(s), &tools); err != nil || tools == nil || len(tools) > 32 {
		return errors.New("Needle tools must be a JSON array of at most 32 tools")
	}
	for _, raw := range tools {
		var tool struct {
			Name       string                     `json:"name"`
			Parameters map[string]json.RawMessage `json:"parameters"`
		}
		if err := json.Unmarshal(raw, &tool); err != nil || strings.TrimSpace(tool.Name) == "" || strings.ContainsRune(tool.Name, 0) || tool.Parameters == nil {
			return errors.New("Needle tool must have a nonempty name and parameters object")
		}
	}
	return nil
}
