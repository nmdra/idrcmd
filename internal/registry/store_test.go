package registry

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func fixture(name string) Definition {
	return Definition{Name: name, Description: "Inspect local state", Command: []string{"git", "status", "--short"}}
}
func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "private", "tools.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestParse(t *testing.T) {
	valid := `{"name":"git_status","description":"Show status","command":["git","status"]}`
	if _, err := Parse(strings.NewReader(valid)); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{valid + ` {}`, strings.Replace(valid, `"name":`, `"other":`, 1), `null`, `[]`, strings.Repeat(" ", MaxDefinitionBytes) + valid, strings.Replace(valid, "git_status", "Bad_Name", 1), strings.Replace(valid, "Show status", `bad\ntext`, 1), strings.Replace(valid, `"git","status"`, `"git","{arg}"`, 1)} {
		if _, err := Parse(strings.NewReader(s)); err == nil {
			t.Errorf("accepted malformed input %.100q", s)
		}
	}
}

func TestStorePersistenceTrustAndParameters(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	for _, x := range []struct {
		name    string
		vector  []float32
		trusted bool
	}{{"git_status", []float32{1, 0, 0}, true}, {"other_tool", []float32{0, 1, 0}, true}, {"untrusted", []float32{1, 0, 0}, false}} {
		d := fixture(x.name)
		d.Description = "O'Reilly ?; DROP TABLE registry_tools; --"
		if err := s.Add(ctx, d, x.vector, "model' ? --", x.trusted); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Add(ctx, fixture("git_status"), []float32{1, 0, 0}, "model' ? --", false); err == nil {
		t.Fatal("duplicate accepted")
	}
	got, err := s.Search(ctx, []float32{1, 0, 0}, "model' ? --", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Tool.Name != "git_status" || math.Abs(got[0].Distance) > 1e-6 {
		t.Fatalf("bad ranking: %+v", got)
	}
	if _, err := s.Get(ctx, `' OR 1=1 --`); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("injection get: %v", err)
	}
	if err := s.Disable(ctx, "git_status"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tools, err := s.List(ctx)
	if err != nil || len(tools) != 3 {
		t.Fatalf("list=%v err=%v", tools, err)
	}
	tool, err := s.Get(ctx, "git_status")
	if err != nil || tool.Enabled || !tool.Trusted || tool.ContentHash == "" {
		t.Fatalf("persisted tool=%+v err=%v", tool, err)
	}
	got, err = s.Search(ctx, []float32{1, 0, 0}, "model' ? --", 1)
	if err != nil || len(got) != 1 || got[0].Tool.Name != "other_tool" {
		t.Fatalf("disabled/untrusted retrieval: %+v %v", got, err)
	}
	for _, p := range []string{path, filepath.Dir(path)} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Fatalf("nonprivate %s", p)
		}
	}
}

func TestStaleAndInvalidVectors(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	for _, v := range [][]float32{nil, {0, 0}, {float32(math.NaN())}, {float32(math.Inf(1))}} {
		if err := s.Add(ctx, fixture("invalid"), v, "model", true); err == nil {
			t.Fatal("accepted invalid vector")
		}
	}
	if err := s.Add(ctx, fixture("valid"), []float32{1, 0}, "model", true); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		v     []float32
		model string
	}{{[]float32{1, 0}, "other"}, {[]float32{1}, "model"}} {
		if _, err := s.Search(ctx, test.v, test.model, 5); err == nil {
			t.Fatal("stale query accepted")
		}
		if err := s.Add(ctx, fixture("stale"), test.v, test.model, true); err == nil {
			t.Fatal("mixed index accepted")
		}
	}
	for _, k := range []int{0, 101} {
		if _, err := s.Search(ctx, []float32{1, 0}, "model", k); err == nil {
			t.Fatal("invalid k accepted")
		}
	}
	for _, statement := range []string{`UPDATE registry_tools SET description='tampered'`, `UPDATE registry_tools SET embedding_hash='tampered'`, `UPDATE registry_tools SET dimensions=3`, `UPDATE registry_tools SET index_version='centered'`} {
		t.Run(statement, func(t *testing.T) {
			// Restore canonical row between independent corruption cases.
			if _, err := s.db.ExecContext(ctx, `DELETE FROM registry_tools`); err != nil {
				t.Fatal(err)
			}
			if err := s.Add(ctx, fixture("valid"), []float32{1, 0}, "model", true); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Search(ctx, []float32{1, 0}, "model", 5); err == nil {
				t.Fatal("tampered catalog searched")
			}
		})
	}
}

func TestMigrationRejectsUnknownAndProbe(t *testing.T) {
	ctx := context.Background()
	for _, future := range []bool{false, true} {
		s, path := openTest(t)
		statement := `DROP TABLE schema_migrations`
		if future {
			statement = `UPDATE schema_migrations SET version=99`
		}
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
		s.Close()
		if other, err := Open(ctx, path); err == nil {
			other.Close()
			t.Fatal("incompatible database opened")
		}
	}
}

func TestUnsafePaths(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	for _, path := range []string{"relative.db", "file:/tmp/x.db", filepath.Join(dir, "x.db") + "?mode=ro", dir + "/../x.db", dir} {
		if s, err := Open(ctx, path); err == nil {
			s.Close()
			t.Fatalf("unsafe path accepted %s", path)
		}
	}
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(ctx, filepath.Join(link, "db")); err == nil {
		s.Close()
		t.Fatal("symlink ancestor accepted")
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		path := filepath.Join(dir, "db"+strings.ReplaceAll(suffix, "-", ""))
		if err := os.Symlink(filepath.Join(target, "missing"), path+suffix); err != nil {
			t.Fatal(err)
		}
		if s, err := Open(ctx, path); err == nil {
			s.Close()
			t.Fatal("symlink file/sidecar accepted")
		}
	}
	path := filepath.Join(dir, "public.db")
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(ctx, path); err == nil {
		s.Close()
		t.Fatal("public database accepted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if s, err := Open(cancelled, filepath.Join(dir, "cancelled.db")); err == nil {
		s.Close()
		t.Fatal("cancelled open accepted")
	}
}

func TestFreshProcessPersistence(t *testing.T) {
	ctx := context.Background()
	if path := os.Getenv("IDRCMD_REGISTRY_CHILD_DB"); path != "" {
		s, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		got, err := s.Search(ctx, []float32{1, 0}, "model", 5)
		if err != nil || len(got) != 1 || got[0].Tool.Name != "persisted" {
			t.Fatalf("fresh process retrieval: %+v %v", got, err)
		}
		return
	}
	s, path := openTest(t)
	if err := s.Add(ctx, fixture("persisted"), []float32{1, 0}, "model", true); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestFreshProcessPersistence$")
	cmd.Env = append(os.Environ(), "IDRCMD_REGISTRY_CHILD_DB="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fresh process: %v %s", err, out)
	}
}

func TestAddRollsBackIndexOnFailure(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `DROP TABLE registry_tools`); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(ctx, fixture("failed"), []float32{1, 0}, "model", true); err == nil {
		t.Fatal("add unexpectedly succeeded")
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM registry_index`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed add committed index metadata")
	}
}

func TestUnsafeWritableAncestors(t *testing.T) {
	for _, mode := range []os.FileMode{0777, 0770, 0720, 0702} {
		for _, level := range []string{"parent", "grandparent"} {
			t.Run(level+"_"+mode.String(), func(t *testing.T) {
				root := t.TempDir()
				grandparent := filepath.Join(root, "grandparent")
				parent := filepath.Join(grandparent, "parent")
				if err := os.MkdirAll(parent, 0700); err != nil {
					t.Fatal(err)
				}
				changed := grandparent
				if level == "parent" {
					changed = parent
				}
				if err := os.Chmod(changed, mode); err != nil {
					t.Fatal(err)
				}
				defer os.Chmod(changed, 0700)
				if s, err := Open(context.Background(), filepath.Join(parent, "db")); err == nil {
					s.Close()
					t.Fatal("writable ancestor accepted")
				}
				info, err := os.Stat(changed)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != mode {
					t.Fatal("existing ancestor permissions changed")
				}
			})
		}
	}
}

type ancestorInfo struct {
	os.FileInfo
	uid  uint32
	mode os.FileMode
}

func (i ancestorInfo) Sys() any          { return &syscall.Stat_t{Uid: i.uid} }
func (i ancestorInfo) Mode() os.FileMode { return i.mode }

func TestAncestorOwnershipAndStickyException(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	uid := uint32(os.Geteuid())
	other := uid + 1
	if other == 0 {
		other = 1
	}
	for _, tc := range []struct {
		name   string
		uid    uint32
		mode   os.FileMode
		wantOK bool
	}{
		{"current_private", uid, os.ModeDir | 0700, true},
		{"root_shared_read", 0, os.ModeDir | 0755, true},
		{"root_sticky", 0, os.ModeDir | os.ModeSticky | 0777, true},
		{"foreign_private", other, os.ModeDir | 0700, false},
		{"foreign_sticky", other, os.ModeDir | os.ModeSticky | 0777, false},
		{"root_nonsticky", 0, os.ModeDir | 0777, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAncestor(ancestorInfo{FileInfo: info, uid: tc.uid, mode: tc.mode})
			if (err == nil) != tc.wantOK {
				t.Fatalf("validation error=%v wantOK=%v", err, tc.wantOK)
			}
		})
	}
}
