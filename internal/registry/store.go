package registry

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	_ "turso.tech/database/tursogo"
)

//go:embed 001.sql
var initialMigration string

const indexVersion = "raw-v1"
const maxTools = 4096

type Store struct{ db *sql.DB }

func Open(ctx context.Context, path string) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := preparePath(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("turso", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("registry migration: %w", err)
	}
	// Inspect sidecars created by the driver as well as the database itself.
	if err := preparePath(path); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		var tables int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&tables); err != nil {
			return err
		}
		if tables != 0 {
			return errors.New("refusing unrelated database without registry migrations")
		}
		for _, statement := range strings.Split(initialMigration, ";") {
			if strings.TrimSpace(statement) != "" {
				if _, err := tx.ExecContext(ctx, statement); err != nil {
					return err
				}
			}
		}
	} else {
		var count, version int
		if err := tx.QueryRowContext(ctx, `SELECT count(*), coalesce(max(version),0) FROM schema_migrations`).Scan(&count, &version); err != nil {
			return err
		}
		if count != 1 || version != 1 {
			return errors.New("unsupported registry schema version")
		}
	}
	return tx.Commit()
}

func vectorJSON(vector []float32, modelID string) (string, error) {
	if strings.TrimSpace(modelID) == "" || len(modelID) > 512 || !printable(modelID) {
		return "", errors.New("invalid embedding model ID")
	}
	if len(vector) == 0 || len(vector) > 16384 {
		return "", errors.New("embedding dimension must be 1 to 16384")
	}
	var norm float64
	for _, v := range vector {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return "", errors.New("embedding must be finite")
		}
		norm += float64(v) * float64(v)
	}
	if norm == 0 {
		return "", errors.New("embedding must have nonzero norm")
	}
	b, err := json.Marshal(vector)
	return string(b), err
}

func blobHash(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func checkIndex(ctx context.Context, tx *sql.Tx, model string, dim int, create bool) error {
	var actualModel, version string
	var actualDim int
	err := tx.QueryRowContext(ctx, `SELECT model_id, dimensions, index_version FROM registry_index WHERE id=1`).Scan(&actualModel, &actualDim, &version)
	if errors.Is(err, sql.ErrNoRows) {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM registry_tools`).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return errors.New("missing embedding index metadata")
		}
		if create {
			_, err = tx.ExecContext(ctx, `INSERT INTO registry_index VALUES (1,?,?,?)`, model, dim, indexVersion)
			return err
		}
		return nil
	}
	if err != nil {
		return err
	}
	if actualModel != model || actualDim != dim || version != indexVersion {
		return errors.New("stale or incompatible embedding index: model, dimension, or raw-v1 mismatch")
	}
	return nil
}

func (s *Store) Add(ctx context.Context, d Definition, vector []float32, modelID string, trusted bool) error {
	if err := d.Validate(); err != nil {
		return err
	}
	v, err := vectorJSON(vector, modelID)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := checkIndex(ctx, tx, modelID, len(vector), true); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM registry_tools`).Scan(&count); err != nil {
		return err
	}
	if count >= maxTools {
		return errors.New("registry exceeds 4096 tools")
	}
	var blob []byte
	if err := tx.QueryRowContext(ctx, `SELECT vector32(?)`, v).Scan(&blob); err != nil {
		return err
	}
	argv, _ := json.Marshal(d.Command)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `INSERT INTO registry_tools (name,description,command_json,content_hash,trusted,enabled,model_id,dimensions,index_version,embedding_hash,embedding,created_at,updated_at) VALUES (?,?,?,?,?,1,?,?,?,?,?,?,?)`, d.Name, d.Description, string(argv), contentHash(d), trusted, modelID, len(vector), indexVersion, blobHash(blob), blob, now, now)
	if err != nil {
		return fmt.Errorf("add tool (names must be unique): %w", err)
	}
	return tx.Commit()
}

const columns = `name,description,command_json,content_hash,trusted,enabled,model_id,dimensions,index_version,embedding_hash,embedding,created_at,updated_at`

type scanner interface{ Scan(...any) error }

func scanTool(row scanner) (Tool, error) {
	var t Tool
	var command, version, hash string
	var blob []byte
	if err := row.Scan(&t.Name, &t.Description, &command, &t.ContentHash, &t.Trusted, &t.Enabled, &t.ModelID, &t.Dimensions, &version, &hash, &blob, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return t, err
	}
	if err := json.Unmarshal([]byte(command), &t.Command); err != nil {
		return t, err
	}
	if err := t.Definition.Validate(); err != nil {
		return t, err
	}
	if contentHash(t.Definition) != t.ContentHash || blobHash(blob) != hash || version != indexVersion {
		return t, errors.New("stale or corrupted tool content/embedding")
	}
	return t, nil
}

func (s *Store) Get(ctx context.Context, name string) (Tool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Tool{}, err
	}
	defer tx.Rollback()
	t, err := scanTool(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM registry_tools WHERE name=?`, name))
	if err != nil {
		return Tool{}, err
	}
	if err := checkIndex(ctx, tx, t.ModelID, t.Dimensions, false); err != nil {
		return Tool{}, err
	}
	return t, tx.Commit()
}
func (s *Store) List(ctx context.Context) ([]Tool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM registry_tools ORDER BY name LIMIT ?`, maxTools+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tools := []Tool{}
	for rows.Next() {
		t, err := scanTool(rows)
		if err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}
	if len(tools) > maxTools {
		return nil, errors.New("registry tool limit exceeded")
	}
	return tools, rows.Err()
}
func (s *Store) Disable(ctx context.Context, name string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE registry_tools SET enabled=0, updated_at=? WHERE name=?`, time.Now().UTC().Format(time.RFC3339Nano), name)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) Search(ctx context.Context, query []float32, modelID string, k int) ([]Candidate, error) {
	if k < 1 || k > 100 {
		return nil, errors.New("search k must be 1 to 100")
	}
	v, err := vectorJSON(query, modelID)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := checkIndex(ctx, tx, modelID, len(query), false); err != nil {
		return nil, err
	}
	// Validate the bounded eligible catalog before ranking. A stale row must not
	// disappear behind top-K or a metadata filter and silently yield partial results.
	rows, err := tx.QueryContext(ctx, `SELECT `+columns+` FROM registry_tools WHERE trusted=1 AND enabled=1 LIMIT ?`, maxTools+1)
	if err != nil {
		return nil, err
	}
	tools := map[string]Tool{}
	for rows.Next() {
		t, e := scanTool(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		if t.ModelID != modelID || t.Dimensions != len(query) {
			rows.Close()
			return nil, errors.New("stale tool embedding metadata")
		}
		tools[t.Name] = t
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(tools) > maxTools {
		return nil, errors.New("registry tool limit exceeded")
	}
	rows, err = tx.QueryContext(ctx, `SELECT name, vector_distance_cos(embedding,vector32(?)) AS distance FROM registry_tools WHERE trusted=1 AND enabled=1 AND model_id=? AND dimensions=? AND index_version=? ORDER BY distance, name LIMIT ?`, v, modelID, len(query), indexVersion, k)
	if err != nil {
		return nil, err
	}
	candidates := []Candidate{}
	for rows.Next() {
		var name string
		var distance float64
		if err := rows.Scan(&name, &distance); err != nil {
			rows.Close()
			return nil, err
		}
		if math.IsNaN(distance) || math.IsInf(distance, 0) {
			rows.Close()
			return nil, errors.New("invalid cosine distance")
		}
		candidates = append(candidates, Candidate{Tool: tools[name], Distance: distance})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return candidates, nil
}
