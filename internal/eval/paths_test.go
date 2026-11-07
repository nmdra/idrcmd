package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFixturePathsRejectGuesses(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "photo.png"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "project"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "photo.png"), filepath.Join(root, "link.png")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		tool, path string
		valid      bool
	}{
		{"image_to_webp", "photo.png", true}, {"compress_video", "video", false},
		{"image_to_webp", "../photo.png", false}, {"image_to_webp", "/etc/passwd", false},
		{"image_to_webp", "link.png", false}, {"image_to_webp", "project", false},
		{"create_archive", "project", true},
	} {
		t.Run(tc.path, func(t *testing.T) {
			value, _ := json.Marshal(tc.path)
			decision := Decision{Outcome: "proposal_validated", Tool: tc.tool, Arguments: map[string]json.RawMessage{"input": value}}
			got := validateFixturePaths(root, decision)
			if (got.Outcome == "proposal_validated") != tc.valid {
				t.Fatalf("unexpected decision: %+v", got)
			}
		})
	}
}
