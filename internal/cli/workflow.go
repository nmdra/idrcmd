package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"
	"unicode"

	"idrcmd/internal/app"
	"idrcmd/internal/config"
	"idrcmd/internal/executor"
	"idrcmd/internal/inference"
	"idrcmd/internal/policy"
	"idrcmd/internal/registry"
)

func workflow(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	if err := ctx.Err(); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	paths, err := config.Resolve()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	flags := flag.NewFlagSet("idrcmd", flag.ContinueOnError)
	flags.SetOutput(errOut)
	dbPath := flags.String("db", paths.Database, "absolute local registry database path")
	modelPath := flags.String("model", paths.Model, "absolute path to pinned Needle 3 weights")
	dryRun := flags.Bool("dry-run", false, "preview without execution")
	explain := flags.Bool("explain", false, "show candidate distances and the resolved action")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(out, usage)
			return 0
		}
		return 2
	}
	remaining := flags.Args()
	if len(remaining) == 0 {
		fmt.Fprintln(errOut, "Provide a natural-language request, or use --help.")
		return 2
	}
	if remaining[0] == "doctor" {
		fmt.Fprintln(errOut, "Put diagnostic options after the doctor subcommand. Use --help.")
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if remaining[0] == "tools" {
		if *dryRun || *explain {
			fmt.Fprintln(errOut, "--dry-run and --explain apply to natural-language requests, not tools management")
			return 2
		}
		return manageTools(ctx, remaining[1:], *dbPath, *modelPath, in, out, errOut)
	}
	request := strings.Join(remaining, " ")
	if err := policy.ValidateRequest(request); err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	store, err := registry.Open(ctx, *dbPath)
	if err != nil {
		fmt.Fprintf(errOut, "Open registry: %v\n", err)
		return 1
	}
	defer store.Close()
	model, _, err := inference.Open(ctx, *modelPath)
	if err != nil {
		fmt.Fprintf(errOut, "Open local model: %v\n", err)
		return 1
	}
	service := app.Service{Model: model, ModelID: inference.ModelID, Repository: store, Runner: executor.Run}
	plan, err := service.Prepare(ctx, request)
	if err != nil {
		fmt.Fprintf(errOut, "No action executed: %v\n", err)
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if err := preview(out, plan.Tool.Name, plan.Tool.Description, plan.Invocation, cwd); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if *explain {
		fmt.Fprintln(out, "Retrieved candidates (cosine distance is not a confidence probability):")
		for _, candidate := range plan.Candidates {
			fmt.Fprintf(out, "  %s: %.6f\n", candidate.Tool.Name, candidate.Distance)
		}
		fmt.Fprintln(out, "Needle proposed one shortlisted tool. Go permits only the reviewed fixed argv shown above.")
	}
	if *dryRun {
		fmt.Fprintln(out, "Dry run: nothing was executed.")
		return 0
	}
	approved, err := confirm(ctx, in, out, "Execute this read-only command? [y/N] ")
	if err != nil {
		fmt.Fprintf(errOut, "Confirmation failed: %v\n", err)
		return 1
	}
	if !approved {
		fmt.Fprintln(out, "Cancelled. Nothing was executed.")
		return 0
	}
	result, err := service.Execute(ctx, plan, cwd)
	if result.Stdout != "" {
		fmt.Fprint(out, safeOutput(result.Stdout))
	}
	if result.Stderr != "" {
		fmt.Fprint(errOut, safeOutput(result.Stderr))
	}
	if result.Truncated {
		fmt.Fprintln(errOut, "Command output was truncated at the configured limit.")
	}
	if err != nil {
		fmt.Fprintf(errOut, "Command failed: %v\n", err)
		if result.ExitCode > 0 && result.ExitCode < 256 {
			return result.ExitCode
		}
		return 1
	}
	if result.Stdout != "" && !strings.HasSuffix(result.Stdout, "\n") {
		fmt.Fprintln(out)
	}
	fmt.Fprintln(out, "Completed.")
	return 0
}

func manageTools(ctx context.Context, args []string, dbPath, modelPath string, in io.Reader, out, errOut io.Writer) int {
	if len(args) == 2 && args[0] == "add" {
		return addTool(ctx, args[1], dbPath, modelPath, in, out, errOut)
	}
	if !(len(args) == 1 && args[0] == "list") && !(len(args) == 2 && (args[0] == "show" || args[0] == "disable")) {
		fmt.Fprintln(errOut, "Usage: tools add FILE.json | tools list | tools show NAME | tools disable NAME")
		return 2
	}
	store, err := registry.Open(ctx, dbPath)
	if err != nil {
		fmt.Fprintf(errOut, "Open registry: %v\n", err)
		return 1
	}
	defer store.Close()
	switch args[0] {
	case "list":
		tools, err := store.List(ctx)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		if len(tools) == 0 {
			fmt.Fprintln(out, "No tools registered. Use tools add FILE.json.")
		}
		for _, tool := range tools {
			fmt.Fprintf(out, "%s  trusted=%t enabled=%t  %q\n", tool.Name, tool.Trusted, tool.Enabled, tool.Description)
		}
	case "show":
		tool, err := store.Get(ctx, args[1])
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(tool); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
	case "disable":
		if err := store.Disable(ctx, args[1]); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		fmt.Fprintf(out, "Disabled %q.\n", args[1])
	}
	return 0
}

func addTool(ctx context.Context, path, dbPath, modelPath string, in io.Reader, out, errOut io.Writer) int {
	definition, err := readDefinition(path)
	if err != nil {
		fmt.Fprintf(errOut, "Read definition: %v\n", err)
		return 2
	}
	invocation, err := policy.Prepare(definition.Command)
	if err != nil {
		fmt.Fprintf(errOut, "Definition rejected: %v\n", err)
		return 2
	}
	if err := preview(out, definition.Name, definition.Description, invocation, ""); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintln(out, "Read-only scope. This exact executable and fixed argv will be indexed locally. No network access is needed.")
	approved, err := confirm(ctx, in, out, "Trust and enable this reviewed command? [y/N] ")
	if err != nil {
		fmt.Fprintf(errOut, "Trust confirmation failed: %v\n", err)
		return 1
	}
	if !approved {
		fmt.Fprintln(out, "Registration cancelled. Nothing was saved.")
		return 0
	}
	model, _, err := inference.Open(ctx, modelPath)
	if err != nil {
		fmt.Fprintf(errOut, "Open local model: %v\n", err)
		return 1
	}
	vector, _, err := model.Embed(ctx, definition.Name+": "+definition.Description)
	if err != nil {
		fmt.Fprintf(errOut, "Index command: %v\n", err)
		return 1
	}
	store, err := registry.Open(ctx, dbPath)
	if err != nil {
		fmt.Fprintf(errOut, "Open registry: %v\n", err)
		return 1
	}
	defer store.Close()
	if err := store.Add(ctx, definition, vector, inference.ModelID, true); err != nil {
		fmt.Fprintf(errOut, "Save tool: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "Registered and indexed %q. Try a natural-language request.\n", definition.Name)
	return 0
}

func readDefinition(path string) (registry.Definition, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return registry.Definition{}, err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return registry.Definition{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return registry.Definition{}, errors.New("definition must be a regular JSON file of at most 64 KiB")
	}
	return registry.Parse(file)
}

func preview(out io.Writer, name, description string, invocation policy.Invocation, cwd string) error {
	argv := append([]string{invocation.Executable}, invocation.Args...)
	encoded, err := json.Marshal(argv)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Tool: %q\nDescription: %q\nExecutable and argv: %s\n", name, description, encoded); err != nil {
		return err
	}
	if cwd != "" {
		_, err = fmt.Fprintf(out, "Working directory: %q\n", cwd)
	}
	return err
}

func confirm(ctx context.Context, in io.Reader, out io.Writer, prompt string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if _, err := fmt.Fprint(out, prompt); err != nil {
		return false, err
	}
	type answer struct {
		text string
		err  error
	}
	answers := make(chan answer, 1)
	// Generic readers have no cancellation API. At most one bounded reader
	// goroutine is used per CLI invocation; cancellation returns to main, which exits.
	go func() {
		scanner := bufio.NewScanner(io.LimitReader(in, 4097))
		scanner.Buffer(make([]byte, 256), 4096)
		if scanner.Scan() {
			answers <- answer{text: scanner.Text()}
			return
		}
		answers <- answer{err: scanner.Err()}
	}()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case value := <-answers:
		if value.err != nil {
			return false, value.err
		}
		text := strings.ToLower(strings.TrimSpace(value.text))
		return text == "y" || text == "yes", nil
	}
}

// Tool output is data, never terminal control or a new model instruction.
func safeOutput(text string) string {
	var result strings.Builder
	for _, r := range text {
		if r == '\n' || r == '\t' || (!unicode.IsControl(r) && !unicode.In(r, unicode.Cf)) {
			result.WriteRune(r)
		} else {
			fmt.Fprintf(&result, "\\u%04x", r)
		}
	}
	return result.String()
}
