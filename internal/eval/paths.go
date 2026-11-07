package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Only inspect files inside this run's private fixture directory. This is an
// evaluation guard, not the future production path/overwrite policy.
func validateFixturePaths(root string, decision Decision) Decision {
	if decision.Outcome != "proposal_validated" && decision.Outcome != "needs_input" {
		return decision
	}
	for _, field := range []string{"input", "directory"} {
		raw, ok := decision.Arguments[field]
		if !ok {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return Decision{Outcome: "rejected", Reason: "path argument is not a string"}
		}
		// All fixture paths are single local names. No filesystem discovery or
		// guessed path resolution is permitted in this diagnostic.
		if value == "." || value == ".." || filepath.Base(value) != value || filepath.IsAbs(value) {
			return Decision{Outcome: "rejected", Reason: "path is outside the fixture scope"}
		}
		info, err := os.Lstat(filepath.Join(root, value))
		if err != nil || info.Mode()&os.ModeSymlink != 0 || (field == "directory" && !info.IsDir()) || (field == "input" && !info.Mode().IsRegular() && !(decision.Tool == "create_archive" && info.IsDir())) {
			return Decision{Outcome: "rejected", Reason: "model path does not identify an existing allowed fixture: " + field}
		}
	}
	return decision
}
