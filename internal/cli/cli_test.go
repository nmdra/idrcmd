package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idrcmd/internal/storage/turso"
)

func TestHelp(t *testing.T) {
	var out, errs bytes.Buffer
	if code := Run(context.Background(), []string{"--help"}, &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s", code, &errs)
	}
	if !strings.Contains(out.String(), "not available") {
		t.Fatal("help must describe the implementation boundary")
	}
}

func TestRejectUnsupported(t *testing.T) {
	for _, args := range [][]string{
		nil, {"--unknown"}, {"tools"}, {"tools", "add"}, {"tools", "reindex"},
		{"doctor", "scenarios"}, {"doctor", "scenarios", "--unknown"},
		{"doctor", "scenarios", "--model", "unused", "extra"},
		{"doctor"}, {"doctor", "needle"}, {"doctor", "needle", "--unknown"},
		{"doctor", "needle", "--model", "unused", "extra"},
		{"doctor", "turso"}, {"doctor", "turso", "--unknown"},
		{"doctor", "turso", "--db", "/tmp/unused.db", "extra"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out, errs bytes.Buffer
			if code := Run(context.Background(), args, &out, &errs); code != 2 {
				t.Fatalf("exit %d, want usage error: %s", code, &errs)
			}
			if out.Len() != 0 {
				t.Fatal("invalid request produced success output")
			}
		})
	}
}

func TestDoctorRealTurso(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probe.db")
	var out, errs bytes.Buffer
	args := []string{"doctor", "turso", "--db", path}
	if code := Run(context.Background(), args, &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s", code, &errs)
	}
	var result turso.Result
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.PersistenceVerified || result.Rows != 10 {
		t.Fatalf("incomplete result: %+v", result)
	}
	out.Reset()
	if code := Run(context.Background(), args, &out, &errs); code != 1 {
		t.Fatalf("existing database: exit %d", code)
	}
}

func TestNeedleDoctorFailsClosed(t *testing.T) {
	var out, errs bytes.Buffer
	if code := Run(context.Background(), []string{"doctor", "needle", "--model", filepath.Join(t.TempDir(), "missing.cact")}, &out, &errs); code != 1 {
		t.Fatalf("missing native model: exit %d", code)
	}
	if out.Len() != 0 || !strings.Contains(errs.String(), "Needle probe failed") {
		t.Fatal("failed native probe must not return success evidence")
	}
}

func TestScenarioDoctorFailsClosed(t *testing.T) {
	var out, errs bytes.Buffer
	if code := Run(context.Background(), []string{"doctor", "scenarios", "--model", filepath.Join(t.TempDir(), "missing.cact")}, &out, &errs); code != 1 {
		t.Fatalf("missing native model: exit %d", code)
	}
	if out.Len() != 0 || !strings.Contains(errs.String(), "Scenario model load failed") {
		t.Fatal("failed scenario probe must not return success evidence")
	}
}

func TestCancelledDoctorDoesNotCreate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(t.TempDir(), "probe.db")
	var out, errs bytes.Buffer
	if code := Run(ctx, []string{"doctor", "turso", "--db", path}, &out, &errs); code != 1 {
		t.Fatalf("cancelled: exit %d", code)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cancelled doctor created database: %v", err)
	}
}
