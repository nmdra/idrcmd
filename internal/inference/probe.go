// Package inference provides the bounded, offline Needle 3 feasibility probe.
// It does not execute commands or grant authority to model output.
package inference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

const ModelID = "needle3:821ce52bd5f596fe880b8400bf53cc671922ad04:c9d915eca282ed42d1a09b143b592adb4cc6744ffe2d294adf5cfc5548170c38"
const modelSHA256 = "c9d915eca282ed42d1a09b143b592adb4cc6744ffe2d294adf5cfc5548170c38"
const modelSize = 35335380

var ErrUnavailable = errors.New("Needle native inference unavailable: run scripts/setup-native.sh, then build on Linux amd64 with CGO_ENABLED=1 and -tags needle")

// ProbeResult contains actual native outputs. Embedding is omitted from JSON to
// avoid a large diagnostic dump, but is available for local Turso integration.
type ProbeResult struct {
	ModelID   string    `json:"model_id"`
	Embedding []float32 `json:"-"`
	Proposal  string    `json:"proposal"`
}

// Probe loads the pinned model, embeds a fixed intent, and asks for one
// read-only git_status proposal. It never runs git. The process-global native
// lifecycle is serialized. The pinned ABI cannot interrupt a C call: context
// cancellation is checked before and after each call, not during it.
func Probe(ctx context.Context, modelPath string) (ProbeResult, error) {
	if err := ctx.Err(); err != nil {
		return ProbeResult{}, err
	}
	return probe(ctx, modelPath)
}

func validateEmbedding(v []float32) error {
	if len(v) == 0 {
		return errors.New("Needle returned an empty embedding")
	}
	var norm float64
	for _, x := range v {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return errors.New("Needle returned a non-finite embedding")
		}
		norm += float64(x) * float64(x)
	}
	if norm == 0 {
		return errors.New("Needle returned a zero embedding")
	}
	return nil
}

func validateProposal(raw string) error {
	var response struct {
		Type          string          `json:"type"`
		Success       bool            `json:"success"`
		Error         json.RawMessage `json:"error"`
		ErrorCode     json.RawMessage `json:"error_code"`
		FunctionCalls []struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"function_calls"`
	}
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		return fmt.Errorf("decode Needle proposal: %w", err)
	}
	if response.Type != "call" || !response.Success || strings.TrimSpace(string(response.Error)) != "null" || strings.TrimSpace(string(response.ErrorCode)) != "null" {
		return errors.New("Needle did not return a successful call envelope")
	}
	if len(response.FunctionCalls) != 1 || response.FunctionCalls[0].Name != "git_status" {
		return errors.New("Needle did not propose exactly one argument-free git_status call")
	}
	var arguments map[string]json.RawMessage
	if err := json.Unmarshal(response.FunctionCalls[0].Arguments, &arguments); err != nil || arguments == nil || len(arguments) != 0 {
		return errors.New("Needle git_status arguments must be an explicit empty object")
	}
	return nil
}
