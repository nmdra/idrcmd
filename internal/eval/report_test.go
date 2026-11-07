package eval

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestMatchesDecodedArguments(t *testing.T) {
	expected := Scenario{Tool: "image_to_webp", Outcome: "proposal_validated", Arguments: map[string]any{"input": "photo.png"}}
	actual := Decision{Tool: "image_to_webp", Outcome: "proposal_validated", Arguments: map[string]json.RawMessage{"input": json.RawMessage(`"photo\u002epng"`)}}
	if !matches(expected, actual) {
		t.Fatal("equivalent JSON strings scored as a mismatch")
	}
}

type failingModel struct{ fixtureModel }

func (m *failingModel) Complete(ctx context.Context, request, schema string) (string, time.Duration, error) {
	if m.completeCalls > 0 {
		return "", 0, errors.New("injected completion failure")
	}
	return m.fixtureModel.Complete(ctx, request, schema)
}
func TestRunPreservesPartialEvidence(t *testing.T) {
	report, err := Run(context.Background(), &failingModel{}, "fake-model-for-error-test")
	if err == nil || report.Complete || len(report.Observations) != 1 || report.TotalScenarios != len(Scenarios()) || report.Passed != 1 {
		t.Fatalf("partial report lost or marked complete: %+v, %v", report, err)
	}
}
