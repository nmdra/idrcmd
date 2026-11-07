//go:build needle && cgo && linux && amd64

package inference

import (
	"context"
	"errors"
	"os"
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
