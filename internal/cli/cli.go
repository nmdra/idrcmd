// Package cli exposes diagnostics and the reviewed read-only user workflow.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"idrcmd/internal/eval"
	"idrcmd/internal/inference"
	"idrcmd/internal/storage/turso"
)

const usage = `idrcmd: Remember the intent, not the command.

Current stage: reviewed fixed-argv read-only tools. Placeholders and write actions are not available.

Usage:
  idrcmd --help
  idrcmd [--db PATH] [--model PATH] tools add FILE.json
  idrcmd [--db PATH] tools list | tools show NAME | tools disable NAME
  idrcmd [--db PATH] [--model PATH] [--dry-run] [--explain] <natural-language request>
  idrcmd doctor turso --db /absolute/path/to/NEW-probe.db
  idrcmd doctor needle --model /path/to/pinned/needle3.cact
  idrcmd doctor scenarios --model /path/to/pinned/needle3.cact

The Turso probe creates a new local database with ten synthetic vectors,
checks SQL cosine ranking, transactions, and close/reopen persistence.
It does not use Needle embeddings or execute registered commands.
The parent directory must exist and be under your control.
The Needle probe requires a native build. It embeds a fixed request and validates
one read-only proposal. It does not execute that proposal.
`

// Run returns a process exit code and reads explicit trust/execution approval from stdin.
func Run(ctx context.Context, args []string, out, errOut io.Writer) int {
	return RunWithInput(ctx, args, os.Stdin, out, errOut)
}

// RunWithInput is the testable CLI boundary. There is no automatic approval flag.
func RunWithInput(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	out, errOut = escapedStream{out}, escapedStream{errOut}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(out, usage)
		return 0
	}
	if len(args) >= 2 && args[0] == "doctor" && args[1] == "scenarios" {
		return scenarioDoctor(ctx, args[2:], out, errOut)
	}
	if len(args) >= 2 && args[0] == "doctor" && args[1] == "needle" {
		return needleDoctor(ctx, args[2:], out, errOut)
	}
	if len(args) < 2 || args[0] != "doctor" || args[1] != "turso" {
		if len(args) > 0 && args[0] == "doctor" {
			fmt.Fprintln(errOut, "Unknown diagnostic. Use --help.")
			return 2
		}
		return workflow(ctx, args, in, out, errOut)
	}
	flags := flag.NewFlagSet("doctor turso", flag.ContinueOnError)
	flags.SetOutput(errOut)
	path := flags.String("db", "", "absolute path to a new local probe database (required)")
	if err := flags.Parse(args[2:]); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *path == "" || flags.NArg() != 0 {
		fmt.Fprintln(errOut, "doctor turso requires --db and accepts no positional arguments")
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := turso.Probe(ctx, *path)
	if encodeErr := json.NewEncoder(out).Encode(result); encodeErr != nil {
		fmt.Fprintf(errOut, "write diagnostic result: %v\n", encodeErr)
		return 1
	}
	if err != nil {
		fmt.Fprintf(errOut, "Turso probe failed: %v\n", err)
		return 1
	}
	return 0
}

func scenarioDoctor(ctx context.Context, args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("doctor scenarios", flag.ContinueOnError)
	flags.SetOutput(errOut)
	path := flags.String("model", "", "path to pinned Needle 3 weights (required)")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *path == "" || flags.NArg() != 0 {
		fmt.Fprintln(errOut, "doctor scenarios requires --model and accepts no positional arguments")
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	model, load, err := inference.Open(ctx, *path)
	if err != nil {
		fmt.Fprintf(errOut, "Scenario model load failed: %v\n", err)
		return 1
	}
	report, runErr := eval.Run(ctx, model, inference.ModelID)
	result := struct {
		eval.Report
		Load             inference.LoadMetrics `json:"load"`
		OperationalError string                `json:"operational_error,omitempty"`
	}{Report: report, Load: load}
	if runErr != nil {
		result.OperationalError = runErr.Error()
	}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		fmt.Fprintf(errOut, "write scenario result: %v\n", err)
		return 1
	}
	if runErr != nil {
		fmt.Fprintf(errOut, "Scenario evaluation incomplete: %v\n", runErr)
		return 1
	}
	if report.Failed > 0 {
		fmt.Fprintf(errOut, "%d of %d scenarios did not meet their fixed expectations\n", report.Failed, len(report.Observations))
		return 1
	}
	return 0
}

func needleDoctor(ctx context.Context, args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("doctor needle", flag.ContinueOnError)
	flags.SetOutput(errOut)
	path := flags.String("model", "", "path to pinned Needle 3 weights (required)")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *path == "" || flags.NArg() != 0 {
		fmt.Fprintln(errOut, "doctor needle requires --model and accepts no positional arguments")
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	start := time.Now()
	result, err := inference.Probe(ctx, *path)
	if err != nil {
		fmt.Fprintf(errOut, "Needle probe failed: %v\n", err)
		return 1
	}
	report := struct {
		inference.ProbeResult
		Dimensions int           `json:"dimensions"`
		Duration   time.Duration `json:"duration_ns"`
	}{result, len(result.Embedding), time.Since(start)}
	if err := json.NewEncoder(out).Encode(report); err != nil {
		fmt.Fprintf(errOut, "write diagnostic result: %v\n", err)
		return 1
	}
	return 0
}
