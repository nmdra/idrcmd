//go:build needle && cgo && linux && amd64

package inference

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"syscall"
	"testing"
	"time"
)

func TestNativeProbe(t *testing.T) {
	path := os.Getenv("IDRCMD_NEEDLE_MODEL")
	if path == "" {
		t.Skip("set IDRCMD_NEEDLE_MODEL to the pinned real needle3.cact to run native inference")
	}
	for i := 0; i < 2; i++ {
		result, err := Probe(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		if result.ModelID != ModelID {
			t.Fatal("wrong model ID")
		}
		if err := validateEmbedding(result.Embedding); err != nil {
			t.Fatal(err)
		}
		if err := validateProposal(result.Proposal); err != nil {
			t.Fatal(err)
		}
		t.Logf("real native probe %d: embedding dimension=%d, proposal=%s", i+1, len(result.Embedding), result.Proposal)
	}
}

func TestNativeCancelledWaiter(t *testing.T) {
	nativeGate <- struct{}{}
	defer func() { <-nativeGate }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := Probe(ctx, "unused"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled waiter: %v", err)
	}
}

func TestNativeRejectsFIFO(t *testing.T) {
	path := t.TempDir() + "/weights.fifo"
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Probe(context.Background(), path); err == nil {
		t.Fatal("accepted non-regular weights")
	}
}

func TestNativeRejectsWrongWeights(t *testing.T) {
	path := t.TempDir() + "/wrong.cact"
	if err := os.WriteFile(path, []byte("not weights"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Probe(context.Background(), path); err == nil {
		t.Fatal("accepted wrong weights")
	}
}

func TestNativeModelOperations(t *testing.T) {
	path := os.Getenv("IDRCMD_NEEDLE_MODEL")
	if path == "" {
		t.Skip("set IDRCMD_NEEDLE_MODEL to run real native operations")
	}
	ctx := context.Background()
	m, metrics, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.VerifyDuration <= 0 {
		t.Fatalf("verification not measured: %+v", metrics)
	}
	_, warm, err := Open(ctx, path)
	if err != nil || warm.VerifyDuration <= 0 || warm.NativeLoadDuration != 0 {
		t.Fatalf("warm open: %+v, %v", warm, err)
	}
	v, duration, err := m.Embed(ctx, "Show my uncommitted changes")
	if err != nil || duration <= 0 {
		t.Fatalf("Embed: %v %v", duration, err)
	}
	if err := validateEmbedding(v); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, request, tools string }{
		{"git", probeIntent, probeTools},
		{"port", "Which program uses port 8080?", `[{"name":"inspect_port","description":"Inspect the process using a TCP port","parameters":{"type":"object","properties":{"port":{"type":"integer"}}}}]`},
		{"missing", "Compress a video", `[{"name":"compress_video","description":"Compress a video file","parameters":{"type":"object","properties":{"input":{"type":"string"}}}}]`},
		{"unsupported", "Book me a flight to Paris", probeTools},
		{"git_again", probeIntent, probeTools},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, duration, err := m.Complete(ctx, tc.request, tc.tools)
			if err != nil || duration <= 0 {
				t.Fatalf("Complete: %v %v", duration, err)
			}
			var response struct {
				Success       bool `json:"success"`
				FunctionCalls []struct {
					Name      string                     `json:"name"`
					Arguments map[string]json.RawMessage `json:"arguments"`
				} `json:"function_calls"`
			}
			if err := json.Unmarshal([]byte(raw), &response); err != nil || !response.Success {
				t.Fatalf("invalid envelope: %s (%v)", raw, err)
			}
			t.Logf("real native outcome: %s", raw)
			if tc.name == "git" || tc.name == "git_again" {
				if err := validateProposal(raw); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "port" && (len(response.FunctionCalls) != 1 || response.FunctionCalls[0].Name != "inspect_port") {
				t.Fatalf("schema change not applied: %s", raw)
			}
			if tc.name == "missing" {
				// This adapter exposes proposals, not grounded or authorized
				// arguments. Record model quality without hiding unsafe output.
				for _, call := range response.FunctionCalls {
					if len(call.Arguments) != 0 {
						t.Logf("missing-input limitation: model proposed arguments that require Go grounding: %s", raw)
					}
				}
			}
			if tc.name == "unsupported" && len(response.FunctionCalls) != 0 {
				t.Fatalf("unsupported intent proposed tool: %s", raw)
			}
		})
	}
	after, _, err := m.Embed(ctx, "Show my uncommitted changes")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v, after) {
		t.Fatal("embedding changed after tool schema/conversation changes")
	}
}
