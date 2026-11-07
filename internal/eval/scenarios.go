package eval

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"syscall"
	"time"

	_ "turso.tech/database/tursogo"
)

// Model is the local inference boundary. Tests may use fakes, but only explicit
// native runs establish real-model evidence.
type Model interface {
	Embed(context.Context, string) ([]float32, time.Duration, error)
	Complete(context.Context, string, string) (string, time.Duration, error)
}

type Candidate struct {
	Name     string  `json:"name"`
	Distance float64 `json:"cosine_distance"`
}

type Observation struct {
	Scenario
	Candidates        []Candidate   `json:"candidates"`
	Decision          Decision      `json:"decision"`
	RawProposal       string        `json:"raw_proposal"`
	EmbedDuration     time.Duration `json:"embed_duration_ns"`
	QueryDuration     time.Duration `json:"sql_query_duration_ns"`
	CompleteDuration  time.Duration `json:"complete_duration_ns"`
	RetrievedExpected bool          `json:"retrieved_expected"`
	Passed            bool          `json:"passed"`
}

type Report struct {
	Complete             bool          `json:"complete"`
	TotalScenarios       int           `json:"total_scenarios"`
	ModelID              string        `json:"model_id"`
	Platform             string        `json:"platform"`
	GoVersion            string        `json:"go_version"`
	CatalogSHA256        string        `json:"catalog_sha256"`
	ScenariosSHA256      string        `json:"scenarios_sha256"`
	CatalogSize          int           `json:"catalog_size"`
	Dimensions           int           `json:"dimensions"`
	IndexBuildDuration   time.Duration `json:"index_build_duration_ns"`
	CatalogEmbedDuration time.Duration `json:"catalog_embed_duration_ns"`
	PeakRSSKiB           int64         `json:"process_peak_rss_kib"`
	SupportedCases       int           `json:"supported_cases"`
	RecallAt5Hits        int           `json:"recall_at_5_hits"`
	Passed               int           `json:"passed"`
	Failed               int           `json:"failed"`
	Observations         []Observation `json:"observations"`
}

// Run builds an isolated local ten-tool catalog using real model descriptions,
// searches SQL top-5 BEFORE calling the model with only retrieved schemas, then
// validates its proposal in Go. It never invokes a registered executable.
// No existing database is modified. Raw outputs refer only to public fixtures.
func Run(ctx context.Context, model Model, modelID string) (Report, error) {
	catalogBytes, err := json.Marshal(Catalog())
	if err != nil {
		return Report{}, err
	}
	scenarioBytes, err := json.Marshal(Scenarios())
	if err != nil {
		return Report{}, err
	}
	report := Report{ModelID: modelID, Platform: runtime.GOOS + "/" + runtime.GOARCH, GoVersion: runtime.Version(), CatalogSize: len(Catalog()), CatalogSHA256: fmt.Sprintf("%x", sha256.Sum256(catalogBytes)), ScenariosSHA256: fmt.Sprintf("%x", sha256.Sum256(scenarioBytes))}
	report.TotalScenarios = len(Scenarios())
	if modelID == "" {
		return report, fmt.Errorf("model ID is required")
	}
	dir, err := os.MkdirTemp("", "idrcmd-scenarios-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(dir)
	// Empty local files exercise path grounding only, not media conversion.
	for _, name := range []string{"photo.png", "holiday.mp4", "interview.mp4"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			return report, err
		}
	}
	for _, name := range []string{"project", "Downloads"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
			return report, err
		}
	}
	path := filepath.Join(dir, "scenarios.db")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return report, err
	}
	if err := file.Close(); err != nil {
		return report, err
	}
	db, err := sql.Open("turso", path)
	if err != nil {
		return report, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `CREATE TABLE m0_scenario_catalog (name TEXT PRIMARY KEY, definition TEXT NOT NULL, model_id TEXT NOT NULL, dimensions INTEGER NOT NULL, index_version TEXT NOT NULL, trusted INTEGER NOT NULL, enabled INTEGER NOT NULL, embedding BLOB NOT NULL)`); err != nil {
		return report, err
	}
	start := time.Now()
	for _, tool := range Catalog() {
		vector, elapsed, err := model.Embed(ctx, tool.Name+": "+tool.Description)
		if err != nil {
			return report, fmt.Errorf("embed catalog %s: %w", tool.Name, err)
		}
		report.CatalogEmbedDuration += elapsed
		if report.Dimensions == 0 {
			report.Dimensions = len(vector)
		}
		if err := checkVector(vector, report.Dimensions); err != nil {
			return report, err
		}
		encoded, err := json.Marshal(vector)
		if err != nil {
			return report, err
		}
		definition, err := json.Marshal(tool)
		if err != nil {
			return report, err
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO m0_scenario_catalog VALUES (?, ?, ?, ?, ?, ?, ?, vector32(?))`, tool.Name, string(definition), modelID, len(vector), "raw-v1", 1, 1, string(encoded)); err != nil {
			return report, err
		}
	}
	// Sentinel rows deliberately reuse a real vector. They must never enter a
	// shortlist, even when that vector is nearest. They are not extra indexed tools.
	for _, sentinel := range []struct {
		name             string
		trusted, enabled int
	}{{"untrusted_sentinel", 0, 1}, {"disabled_sentinel", 1, 0}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO m0_scenario_catalog SELECT ?, definition, model_id, dimensions, index_version, ?, ?, embedding FROM m0_scenario_catalog WHERE name = ?`, sentinel.name, sentinel.trusted, sentinel.enabled, "git_status"); err != nil {
			return report, err
		}
	}
	report.IndexBuildDuration = time.Since(start)
	for _, scenario := range Scenarios() {
		observation := Observation{Scenario: scenario}
		vector, elapsed, err := model.Embed(ctx, scenario.Request)
		if err != nil {
			return report, fmt.Errorf("embed %s: %w", scenario.ID, err)
		}
		observation.EmbedDuration = elapsed
		if err := checkVector(vector, report.Dimensions); err != nil {
			return report, err
		}
		query, err := json.Marshal(vector)
		if err != nil {
			return report, err
		}
		start := time.Now()
		rows, err := db.QueryContext(ctx, `SELECT name, definition, vector_distance_cos(embedding, vector32(?)) AS distance FROM m0_scenario_catalog WHERE trusted = ? AND enabled = ? AND model_id = ? AND dimensions = ? AND index_version = ? ORDER BY distance, name LIMIT ?`, string(query), 1, 1, modelID, report.Dimensions, "raw-v1", 5)
		if err != nil {
			return report, err
		}
		var tools []Tool
		for rows.Next() {
			var candidate Candidate
			var definition string
			if err := rows.Scan(&candidate.Name, &definition, &candidate.Distance); err != nil {
				rows.Close()
				return report, err
			}
			if math.IsNaN(candidate.Distance) || math.IsInf(candidate.Distance, 0) {
				rows.Close()
				return report, fmt.Errorf("non-finite SQL distance")
			}
			var tool Tool
			if err := json.Unmarshal([]byte(definition), &tool); err != nil {
				rows.Close()
				return report, err
			}
			if tool.Name != candidate.Name {
				rows.Close()
				return report, fmt.Errorf("untrusted/disabled or inconsistent catalog row returned")
			}
			tools = append(tools, tool)
			observation.Candidates = append(observation.Candidates, candidate)
			if candidate.Name == scenario.Tool {
				observation.RetrievedExpected = true
			}
		}
		rowErr := rows.Err()
		closeErr := rows.Close()
		if rowErr != nil {
			return report, rowErr
		}
		if closeErr != nil {
			return report, closeErr
		}
		observation.QueryDuration = time.Since(start)
		if len(tools) != 5 {
			return report, fmt.Errorf("expected top-5 eligible fixtures, got %d", len(tools))
		}
		if scenario.Tool != "" {
			report.SupportedCases++
			if observation.RetrievedExpected {
				report.RecallAt5Hits++
			}
		}
		schema, err := schemas(tools)
		if err != nil {
			return report, err
		}
		raw, elapsed, err := model.Complete(ctx, scenario.Request, schema)
		if err != nil {
			return report, fmt.Errorf("complete %s: %w", scenario.ID, err)
		}
		observation.RawProposal = raw
		observation.CompleteDuration = elapsed
		observation.Decision = validate(scenario.Request, raw, tools)
		observation.Decision = validateFixturePaths(dir, observation.Decision)
		observation.Passed = matches(scenario, observation.Decision)
		if observation.Passed {
			report.Passed++
		} else {
			report.Failed++
		}
		report.Observations = append(report.Observations, observation)
	}
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return report, err
	}
	report.PeakRSSKiB = usage.Maxrss
	report.Complete = true
	return report, nil
}

func checkVector(vector []float32, dimensions int) error {
	if dimensions == 0 || len(vector) != dimensions {
		return fmt.Errorf("model/index vector dimension mismatch")
	}
	var norm float64
	for _, v := range vector {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return fmt.Errorf("non-finite embedding")
		}
		norm += float64(v) * float64(v)
	}
	if norm == 0 {
		return fmt.Errorf("zero embedding")
	}
	return nil
}

func matches(expected Scenario, actual Decision) bool {
	if expected.Outcome != actual.Outcome || expected.Tool != actual.Tool {
		return false
	}
	if expected.Arguments == nil {
		return len(actual.Arguments) == 0
	}
	want, err := json.Marshal(expected.Arguments)
	if err != nil {
		return false
	}
	got, err := json.Marshal(actual.Arguments)
	if err != nil {
		return false
	}
	var expectedValues, actualValues any
	if json.Unmarshal(want, &expectedValues) != nil || json.Unmarshal(got, &actualValues) != nil {
		return false
	}
	return reflect.DeepEqual(expectedValues, actualValues)
}
