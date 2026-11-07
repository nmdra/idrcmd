//go:build needle && cgo && linux && amd64

package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"idrcmd/internal/inference"
	_ "turso.tech/database/tursogo"
)

// TestNativeVectorRoundTrip verifies actual Needle vector encoding and Turso
// cosine search in one binary. The query equals the stored vector. This is an
// integration test, not evidence of semantic recall or shortlist routing quality.
// No proposed command is executed.
func TestNativeVectorRoundTrip(t *testing.T) {
	model := os.Getenv("IDRCMD_NEEDLE_MODEL")
	if model == "" {
		t.Skip("set IDRCMD_NEEDLE_MODEL to run the real native integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := inference.Probe(ctx, model)
	if err != nil {
		t.Fatal(err)
	}
	vector, err := json.Marshal(result.Embedding)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "native.db")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("turso", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE TABLE m0_native_vectors (id TEXT PRIMARY KEY, model_id TEXT NOT NULL, embedding BLOB NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO m0_native_vectors VALUES (?, ?, vector32(?))`, "git_status", result.ModelID, string(vector)); err != nil {
		t.Fatal(err)
	}
	checkNativeVector(t, ctx, db, string(vector))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// The fresh process receives the original query vector. It verifies that
	// the model vector survives restart, not a second model inference.
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeVectorRestartReader$")
	cmd.Env = append(os.Environ(), "IDRCMD_TEST_NATIVE_DB="+path, "IDRCMD_TEST_NATIVE_QUERY="+string(vector))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native-vector restart check: %v\n%s", err, output)
	}
	t.Logf("real Needle vector: model=%s dimensions=%d; Turso roundtrip and restart passed; proposal=%s", result.ModelID, len(result.Embedding), result.Proposal)
}

func TestNativeVectorRestartReader(t *testing.T) {
	path := os.Getenv("IDRCMD_TEST_NATIVE_DB")
	if path == "" {
		t.Skip("only runs in the native-vector restart child process")
	}
	db, err := sql.Open("turso", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	checkNativeVector(t, context.Background(), db, os.Getenv("IDRCMD_TEST_NATIVE_QUERY"))
}

func checkNativeVector(t *testing.T, ctx context.Context, db *sql.DB, query string) {
	t.Helper()
	var id string
	var distance float64
	err := db.QueryRowContext(ctx, `SELECT id, vector_distance_cos(embedding, vector32(?)) AS distance FROM m0_native_vectors WHERE model_id = ? ORDER BY distance LIMIT ?`, query, inference.ModelID, 1).Scan(&id, &distance)
	if err != nil {
		t.Fatal(err)
	}
	if id != "git_status" || math.IsNaN(distance) || math.IsInf(distance, 0) || math.Abs(distance) > 1e-5 {
		t.Fatalf("unexpected real-vector result: id=%q distance=%g", id, distance)
	}
}
