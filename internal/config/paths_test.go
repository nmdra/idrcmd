package config

import (
	"path/filepath"
	"testing"
)

func TestXDGPathsAndOverrides(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("IDRCMD_DB", "")
	t.Setenv("IDRCMD_MODEL", "")
	paths, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if paths.Database != filepath.Join(root, ".local", "share", "idrcmd", "idrcmd.db") || paths.Model != filepath.Join(root, ".cache", "idrcmd", "models", "needle3.cact") {
		t.Fatalf("wrong defaults: %+v", paths)
	}
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	paths, err = Resolve()
	if err != nil || paths.Database != filepath.Join(root, "data", "idrcmd", "idrcmd.db") {
		t.Fatalf("wrong XDG paths: %+v %v", paths, err)
	}
	t.Setenv("IDRCMD_DB", filepath.Join(root, "custom.db"))
	t.Setenv("IDRCMD_MODEL", filepath.Join(root, "custom.cact"))
	paths, err = Resolve()
	if err != nil || paths.Database != filepath.Join(root, "custom.db") || paths.Model != filepath.Join(root, "custom.cact") {
		t.Fatalf("wrong overrides: %+v %v", paths, err)
	}
}
func TestRelativePathsRejected(t *testing.T) {
	for _, name := range []string{"XDG_DATA_HOME", "XDG_CACHE_HOME", "IDRCMD_DB", "IDRCMD_MODEL"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", "")
			t.Setenv("XDG_CACHE_HOME", "")
			t.Setenv("IDRCMD_DB", "")
			t.Setenv("IDRCMD_MODEL", "")
			t.Setenv(name, "relative")
			if _, err := Resolve(); err == nil {
				t.Fatalf("accepted relative %s", name)
			}
		})
	}
}
