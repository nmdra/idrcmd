// Package turso contains the isolated M0 local database feasibility probe.
// It does not implement the production command registry.
package turso

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "turso.tech/database/tursogo"
)

// Result records observations, not model retrieval quality. All vectors are fixed
// synthetic three-dimensional fixtures, not Needle embeddings. On failure Probe
// returns partial evidence and an error; false fields are not successful checks.
type Result struct {
	Path                string        `json:"path"`
	DriverVersion       string        `json:"driver_version"`
	VectorSource        string        `json:"vector_source"`
	Dimensions          int           `json:"dimensions"`
	Rows                int           `json:"rows"`
	NearestID           string        `json:"nearest_id"`
	Distance            float64       `json:"distance"`
	QueryDuration       time.Duration `json:"query_duration_ns"`
	CommitVerified      bool          `json:"commit_verified"`
	RollbackVerified    bool          `json:"rollback_verified"`
	PersistenceVerified bool          `json:"persistence_verified"`
}

var samples = [...]struct{ id, vector string }{
	{"sample-01", "[1,0,0]"}, {"sample-02", "[0,1,0]"},
	{"sample-03", "[0,0,1]"}, {"sample-04", "[-1,0,0]"},
	{"sample-05", "[0,-1,0]"}, {"sample-06", "[0,0,-1]"},
	{"sample-07", "[1,1,0]"}, {"sample-08", "[1,0,1]"},
	{"sample-09", "[0,1,1]"}, {"sample-10", "[-1,-1,-1]"},
}

// Probe creates a NEW local database, commits ten synthetic vectors, verifies
// SQL cosine ranking and rollback, then closes and reopens it to check persistence.
// path must be an absolute clean filename in an existing, non-symlink directory
// tree controlled by the caller. URLs, DSN options, existing files (including
// sidecars), and symlinks are refused. The database is exclusively created with
// mode 0600. No directories are created, no files are removed, and retrying the
// same path fails. A failed probe can leave its newly created database for
// diagnosis. Callers must not concurrently replace the path or its ancestors.
// Context bounds SQL work; the pinned native loader and Close are not cancellable.
func Probe(ctx context.Context, path string) (result Result, err error) {
	result = Result{Path: path, DriverVersion: "v0.8.3", VectorSource: "synthetic-fixed-not-needle", Dimensions: 3}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = reserve(path); err != nil {
		return result, err
	}
	db, err := sql.Open("turso", path)
	if err != nil {
		return result, fmt.Errorf("open probe database: %w", err)
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, `CREATE TABLE m0_probe_vectors (id TEXT PRIMARY KEY, embedding BLOB NOT NULL)`); err != nil {
		return result, fmt.Errorf("create M0 probe table: %w", err)
	}
	if err = insertSamples(ctx, db); err != nil {
		return result, err
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM m0_probe_vectors`).Scan(&result.Rows); err != nil {
		return result, fmt.Errorf("count committed samples: %w", err)
	}
	if result.Rows != len(samples) {
		return result, fmt.Errorf("commit verification: got %d rows, want %d", result.Rows, len(samples))
	}
	result.CommitVerified = true
	start := time.Now()
	result.NearestID, result.Distance, err = nearest(ctx, db)
	result.QueryDuration = time.Since(start)
	if err != nil {
		return result, err
	}
	if err = verifyRollback(ctx, db); err != nil {
		return result, err
	}
	result.RollbackVerified = true
	if err = db.Close(); err != nil {
		return result, fmt.Errorf("close before reopen: %w", err)
	}
	reopened, openErr := sql.Open("turso", path)
	if openErr != nil {
		return result, fmt.Errorf("reopen probe database: %w", openErr)
	}
	defer func() { err = errors.Join(err, reopened.Close()) }()
	reopened.SetMaxOpenConns(1)
	var count int
	if err = reopened.QueryRowContext(ctx, `SELECT count(*) FROM m0_probe_vectors`).Scan(&count); err != nil {
		return result, fmt.Errorf("read after reopen: %w", err)
	}
	if count != len(samples) {
		return result, fmt.Errorf("persistence verification: got %d rows, want %d", count, len(samples))
	}
	if _, _, err = nearest(ctx, reopened); err != nil {
		return result, fmt.Errorf("rank after reopen: %w", err)
	}
	if err = absentRollback(ctx, reopened); err != nil {
		return result, err
	}
	result.PersistenceVerified = true
	return result, nil
}

func reserve(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, ":?#\x00\r\n") || path == string(filepath.Separator) {
		return errors.New("probe requires a clean absolute local filename without URL or DSN syntax")
	}
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		if err != nil {
			return fmt.Errorf("inspect probe parent: %w", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("probe parent must be an existing directory without symlinks")
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); err == nil {
			return fmt.Errorf("probe path or sidecar already exists: %w", os.ErrExist)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect probe path: %w", err)
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("reserve new probe database: %w", err)
	}
	return f.Close()
}

func insertSamples(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sample transaction: %w", err)
	}
	defer tx.Rollback()
	for _, sample := range samples {
		if _, err := tx.ExecContext(ctx, `INSERT INTO m0_probe_vectors (id, embedding) VALUES (?, vector32(?))`, sample.id, sample.vector); err != nil {
			return fmt.Errorf("insert synthetic sample %s: %w", sample.id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit samples: %w", err)
	}
	return nil
}

func nearest(ctx context.Context, db *sql.DB) (string, float64, error) {
	var id string
	var distance float64
	err := db.QueryRowContext(ctx, `SELECT id, vector_distance_cos(embedding, vector32(?)) AS distance FROM m0_probe_vectors ORDER BY distance, id LIMIT ?`, "[1,0,0]", 1).Scan(&id, &distance)
	if err != nil {
		return id, distance, fmt.Errorf("SQL vector32/cosine query: %w", err)
	}
	if id != "sample-01" || math.IsNaN(distance) || math.IsInf(distance, 0) || math.Abs(distance) > 1e-6 {
		return id, distance, fmt.Errorf("unexpected nearest synthetic vector: id=%q distance=%g", id, distance)
	}
	return id, distance, nil
}

func verifyRollback(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin rollback transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO m0_probe_vectors (id, embedding) VALUES (?, vector32(?))`, "rollback-marker", "[1,0,0]"); err != nil {
		return fmt.Errorf("insert rollback marker: %w", err)
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM m0_probe_vectors WHERE id = ?`, "rollback-marker").Scan(&count); err != nil {
		return fmt.Errorf("read uncommitted marker: %w", err)
	}
	if count != 1 {
		return errors.New("rollback marker not visible inside transaction")
	}
	if err = tx.Rollback(); err != nil {
		return fmt.Errorf("rollback marker: %w", err)
	}
	return absentRollback(ctx, db)
}

func absentRollback(ctx context.Context, db *sql.DB) error {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM m0_probe_vectors WHERE id = ?`, "rollback-marker").Scan(&count); err != nil {
		return fmt.Errorf("read rolled-back marker: %w", err)
	}
	if count != 0 {
		return errors.New("rollback verification failed: marker persisted")
	}
	return nil
}
