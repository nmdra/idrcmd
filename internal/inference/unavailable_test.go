//go:build !needle || !cgo || !linux || !amd64

package inference

import (
	"context"
	"errors"
	"testing"
)

func TestUnavailableFailsClosed(t *testing.T) {
	result, err := Probe(context.Background(), "not-a-model")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
	if len(result.Embedding) != 0 || result.Proposal != "" {
		t.Fatal("unavailable build returned synthetic output")
	}
}

func TestCancelledProbe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Probe(ctx, "not-a-model"); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
