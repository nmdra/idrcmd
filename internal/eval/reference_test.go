package eval

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// This checks the consistency of a recorded native run. It does not rerun or
// substitute for native inference, and it must not turn observed failures green.
func TestReferenceReportConsistency(t *testing.T) {
	data, err := os.ReadFile("../../tests/results/m0-linux-amd64.json")
	if err != nil {
		t.Fatal(err)
	}
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	catalog, _ := json.Marshal(Catalog())
	scenarios, _ := json.Marshal(Scenarios())
	if report.CatalogSHA256 != fmt.Sprintf("%x", sha256.Sum256(catalog)) || report.ScenariosSHA256 != fmt.Sprintf("%x", sha256.Sum256(scenarios)) {
		t.Fatal("reference fixture hashes do not match current fixtures; rerun real scenarios explicitly")
	}
	if !report.Complete || len(report.Observations) != len(Scenarios()) || report.TotalScenarios != len(Scenarios()) {
		t.Fatal("reference report is incomplete")
	}
	passed, hits, supported := 0, 0, 0
	for i, observation := range report.Observations {
		want, _ := json.Marshal(Scenarios()[i])
		got, _ := json.Marshal(observation.Scenario)
		if string(want) != string(got) {
			t.Fatalf("reference expectation changed at case %d", i)
		}
		if matches(observation.Scenario, observation.Decision) != observation.Passed {
			t.Fatalf("incorrect scoring for %s", observation.ID)
		}
		if observation.Passed {
			passed++
		}
		if observation.Tool != "" {
			supported++
			if observation.RetrievedExpected {
				hits++
			}
		}
	}
	if passed != report.Passed || report.Failed != len(Scenarios())-passed || hits != report.RecallAt5Hits || supported != report.SupportedCases {
		t.Fatal("reference counters do not match observations")
	}
}
