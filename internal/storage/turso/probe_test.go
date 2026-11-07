package turso

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// This test uses the real pinned Turso driver and native vector functions.
// It is not a fake-embedder or mock-database test.
func TestProbeRealDriver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probe.db")
	got, err := Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rows != 10 || got.Dimensions != 3 || got.NearestID != "sample-01" || got.VectorSource != "synthetic-fixed-not-needle" || !got.CommitVerified || !got.RollbackVerified || !got.PersistenceVerified {
		t.Fatalf("incomplete evidence: %+v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("database mode %o, want 600", info.Mode().Perm())
	}
	// Open independently after Probe has returned and closed both handles.
	db, err := sql.Open("turso", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM m0_probe_vectors`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 10 {
		t.Fatalf("persisted count %d, want 10", count)
	}
	if _, err := Probe(context.Background(), path); !errors.Is(err, os.ErrExist) {
		t.Fatalf("repeat error = %v, want existence error", err)
	}
}

func TestProbeRejectsUnsafePaths(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.db")
	if err := os.WriteFile(existing, []byte("do not alter"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.db")
	if err := os.Symlink(existing, link); err != nil {
		t.Fatal(err)
	}
	parentLink := filepath.Join(dir, "linked-dir")
	if err := os.Symlink(dir, parentLink); err != nil {
		t.Fatal(err)
	}
	sidecarPath := filepath.Join(dir, "sidecar.db")
	if err := os.WriteFile(sidecarPath+"-wal", []byte("do not alter"), 0600); err != nil {
		t.Fatal(err)
	}
	paths := []string{"", ":memory:", "relative.db", "file:/tmp/probe.db", "https://example.com/probe.db", "libsql://example.com", filepath.Join(dir, "probe.db") + "?vfs=memory", filepath.Join(dir, "probe#x.db"), dir + "/../probe.db", dir, existing, link, filepath.Join(parentLink, "probe.db"), filepath.Join(dir, "missing", "probe.db"), sidecarPath, "/"}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			if _, err := Probe(context.Background(), path); err == nil {
				t.Fatalf("accepted unsafe path %q", path)
			}
		})
	}
	data, err := os.ReadFile(existing)
	if err != nil || string(data) != "do not alter" {
		t.Fatalf("existing data altered: %q, %v", data, err)
	}
	if _, err := os.Stat(sidecarPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created database beside existing sidecar: %v", err)
	}
}

func TestProbeCancelledBeforeCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probe.db")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Probe(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled probe created file: %v", err)
	}
}
