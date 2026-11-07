package eval

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func envelope(name string, arguments any) string {
	calls := []any{}
	if name != "" {
		calls = append(calls, map[string]any{"name": name, "arguments": arguments})
	}
	data, _ := json.Marshal(map[string]any{"type": "call", "success": true, "error": nil, "error_code": nil, "function_calls": calls})
	return string(data)
}

func TestValidation(t *testing.T) {
	for _, tc := range []struct{ name, request, raw, outcome string }{
		{"valid", "convert photo.png to webp", envelope("image_to_webp", map[string]any{"input": "photo.png"}), "proposal_validated"},
		{"missing", "compress a video", envelope("compress_video", map[string]any{}), "needs_input"},
		{"no-call", "book a flight", envelope("", nil), "no_call"},
		{"invented", "compress a video", envelope("compress_video", map[string]any{"input": "invented.mp4"}), "rejected"},
		{"unknown-arg", "convert photo.png to webp", envelope("image_to_webp", map[string]any{"input": "photo.png", "flag": "-y"}), "rejected"},
		{"unregistered", "delete everything", envelope("delete_files", map[string]any{}), "rejected"},
		{"failure", "git status", `{"type":"call","success":false,"error":"failed","error_code":1,"function_calls":[]}`, "rejected"},
		{"malformed", "git status", `{`, "rejected"},
		{"null-args", "git status", envelope("git_status", nil), "rejected"},
		{"injection", "convert $(touch PWNED).png", envelope("image_to_webp", map[string]any{"input": "$(touch PWNED).png"}), "rejected"},
		{"leading-option", "convert -y to webp", envelope("image_to_webp", map[string]any{"input": "-y"}), "rejected"},
		{"substring-port", "inspect port 8080", envelope("inspect_port", map[string]any{"port": 808}), "rejected"},
		{"out-of-range-port", "inspect port 99999", envelope("inspect_port", map[string]any{"port": 99999}), "rejected"},
		{"shortlist", "git status", envelope("docker_logs", map[string]any{"container": "git"}), "rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tools := Catalog()
			if tc.name == "shortlist" {
				tools = tools[:1]
			}
			got := validate(tc.request, tc.raw, tools)
			if got.Outcome != tc.outcome {
				t.Fatalf("got %+v, want %s", got, tc.outcome)
			}
		})
	}
}

// This fake checks harness plumbing only. It is not native-model evidence.
type fixtureModel struct{ completeCalls int }

func (m *fixtureModel) Embed(_ context.Context, text string) ([]float32, time.Duration, error) {
	name := "git_status"
	for _, tool := range Catalog() {
		if strings.HasPrefix(text, tool.Name+": ") {
			name = tool.Name
		}
	}
	for _, scenario := range Scenarios() {
		if scenario.Request == text && scenario.Tool != "" {
			name = scenario.Tool
		}
	}
	vector := make([]float32, len(Catalog()))
	for i, tool := range Catalog() {
		if tool.Name == name {
			vector[i] = 1
		}
	}
	return vector, time.Microsecond, nil
}
func (m *fixtureModel) Complete(_ context.Context, request, schema string) (string, time.Duration, error) {
	m.completeCalls++
	var tools []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(schema), &tools); err != nil {
		return "", 0, err
	}
	if len(tools) != 5 {
		panic("model must see only the SQL shortlist")
	}
	for _, tool := range tools {
		if strings.Contains(tool.Name, "sentinel") {
			panic("ineligible tool reached model")
		}
	}
	for _, scenario := range Scenarios() {
		if scenario.Request == request {
			args := scenario.Arguments
			if args == nil {
				args = map[string]any{}
			}
			return envelope(scenario.Tool, args), time.Millisecond, nil
		}
	}
	panic("unexpected request")
}
func TestRunnerWithFakeModelAndRealTurso(t *testing.T) {
	model := &fixtureModel{}
	report, err := Run(context.Background(), model, "fake-model-for-plumbing-only")
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 0 || report.Passed != len(Scenarios()) || report.CatalogSize != 10 || report.RecallAt5Hits != report.SupportedCases || model.completeCalls != len(Scenarios()) {
		t.Fatalf("incomplete harness results: %+v", report)
	}
}

func TestGrounding(t *testing.T) {
	for _, tc := range []struct {
		request, value string
		want           bool
	}{
		{"port 8080", "808", false}, {"container capital", "api", false}, {"container api", "api", true},
		{"convert 'photo.png'", "photo.png", true}, {"convert myphoto.png", "photo.png", false},
	} {
		if got := grounded(tc.request, tc.value); got != tc.want {
			t.Fatalf("grounded(%q,%q)=%v", tc.request, tc.value, got)
		}
	}
}
