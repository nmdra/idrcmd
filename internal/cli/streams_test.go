package cli

import (
	"bytes"
	"io"
	"testing"
)

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }

func TestEscapedStream(t *testing.T) {
	var output bytes.Buffer
	data := []byte("file\x1b[2J\n")
	if n, err := (escapedStream{&output}).Write(data); err != nil || n != len(data) {
		t.Fatalf("write: %d %v", n, err)
	}
	if output.String() != "file\\u001b[2J\n" {
		t.Fatalf("terminal control escaped incorrectly: %q", output.String())
	}
	if _, err := (escapedStream{shortWriter{}}).Write(data); err != io.ErrShortWrite {
		t.Fatalf("short writer: %v", err)
	}
}
