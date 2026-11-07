// Package config resolves offline local paths for the first user workflow.
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type Paths struct {
	Database string
	Model    string
}

// Resolve follows XDG defaults. Explicit IDRCMD_DB and IDRCMD_MODEL overrides
// must be absolute. It does not open files, download assets, or create directories.
func Resolve() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		cache = filepath.Join(home, ".cache")
	}
	if !filepath.IsAbs(data) || !filepath.IsAbs(cache) {
		return Paths{}, fmt.Errorf("XDG_DATA_HOME and XDG_CACHE_HOME must be absolute when set")
	}
	paths := Paths{Database: filepath.Join(data, "idrcmd", "idrcmd.db"), Model: filepath.Join(cache, "idrcmd", "models", "needle3.cact")}
	if value := os.Getenv("IDRCMD_DB"); value != "" {
		paths.Database = value
	}
	if value := os.Getenv("IDRCMD_MODEL"); value != "" {
		paths.Model = value
	}
	if !filepath.IsAbs(paths.Database) || !filepath.IsAbs(paths.Model) {
		return Paths{}, fmt.Errorf("IDRCMD_DB and IDRCMD_MODEL must be absolute when set")
	}
	return paths, nil
}
