package inference

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBoundedInputs(t *testing.T) {
	for _, input := range []string{"", " \n", "a\x00b", strings.Repeat("x", maxInputBytes+1)} {
		if err := validateInput(input); err == nil {
			t.Fatalf("accepted invalid input of length %d", len(input))
		}
	}
	if err := validateInput("convert photo.png to WebP"); err != nil {
		t.Fatal(err)
	}
	for _, tools := range []string{"", "null", "{}", "[null]", "[{}]", `[{"name":"x","parameters":null}]`, "[]\x00", strings.Repeat(" ", maxToolsBytes+1)} {
		if err := validateTools(tools); err == nil {
			t.Fatalf("accepted invalid tools of length %d", len(tools))
		}
	}
	for _, tools := range []string{"[]", `[{"name":"x","parameters":{"type":"object","properties":{}}}]`} {
		if err := validateTools(tools); err != nil {
			t.Fatal(err)
		}
	}
}

func TestModelCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := Open(ctx, "unused"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var m *Model
	if _, _, err := m.Embed(ctx, "hello"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, err := m.Complete(ctx, "hello", "[]"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
