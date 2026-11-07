package inference

import (
	"math"
	"testing"
)

func TestValidateProposal(t *testing.T) {
	const ok = `"type":"call","success":true,"error":null,"error_code":null,`
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"expected", `{` + ok + `"function_calls":[{"name":"git_status","arguments":{}}]}`, true},
		{"malformed", `{`, false},
		{"empty", `{` + ok + `"function_calls":[]}`, false},
		{"unregistered", `{` + ok + `"function_calls":[{"name":"delete_files","arguments":{}}]}`, false},
		{"unknown argument", `{` + ok + `"function_calls":[{"name":"git_status","arguments":{"flag":"--delete"}}]}`, false},
		{"multiple", `{` + ok + `"function_calls":[{"name":"git_status"},{"name":"git_status"}]}`, false},
		{"missing envelope", `{"function_calls":[{"name":"git_status","arguments":{}}]}`, false},
		{"failed envelope", `{"type":"error","success":false,"error":"failed","error_code":1,"function_calls":[{"name":"git_status","arguments":{}}]}`, false},
		{"error despite success", `{"type":"call","success":true,"error":"failed","error_code":null,"function_calls":[{"name":"git_status","arguments":{}}]}`, false},
		{"missing arguments", `{` + ok + `"function_calls":[{"name":"git_status"}]}`, false},
		{"null arguments", `{` + ok + `"function_calls":[{"name":"git_status","arguments":null}]}`, false},
		{"array arguments", `{` + ok + `"function_calls":[{"name":"git_status","arguments":[]}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateProposal(tc.raw); (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

func TestValidateEmbedding(t *testing.T) {
	for _, v := range [][]float32{nil, {0, 0}, {float32(math.NaN())}, {float32(math.Inf(1))}} {
		if validateEmbedding(v) == nil {
			t.Fatalf("accepted invalid embedding: %v", v)
		}
	}
	if err := validateEmbedding([]float32{0.1, -0.2}); err != nil {
		t.Fatal(err)
	}
}
