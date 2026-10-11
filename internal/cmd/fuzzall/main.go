// Command fuzzall runs every fuzz target in the module, one after another,
// each for a fixed time.
//
// go test fuzzes only one target per run, so fuzzall first lists the targets
// with go test -json -list and then fuzzes each one separately:
//
//	go run ./internal/cmd/fuzzall [-fuzztime 30s] [packages]
//
// The packages default to ./... . fuzzall exits with 0 when every target ran
// without a failure, 1 when listing or fuzzing failed, and 64 on a usage
// error.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
)

// Exit codes.
const (
	exitOK     = 0
	exitFailed = 1
	exitUsage  = 64
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run parses the arguments, lists the fuzz targets and fuzzes each one,
// writing progress to stdout and errors to stderr. It returns the exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fuzzall", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fuzztime := fs.String("fuzztime", "30s", "how long to fuzz each target: a duration such as 30s, or Nx for N iterations")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	patterns := fs.Args()
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}

	targets, err := listTargets(ctx, patterns, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "fuzzall: %v\n", err)
		return exitFailed
	}
	if len(targets) == 0 {
		fmt.Fprintln(stdout, "fuzzall: no fuzz targets found")
		return exitOK
	}
	for _, t := range targets {
		fmt.Fprintf(stdout, "fuzzall: fuzzing %s in %s for %s\n", t.Name, t.Package, *fuzztime)
		match := "^" + regexp.QuoteMeta(t.Name) + "$"
		cmd := exec.CommandContext(ctx, "go", "test", "-run", "^$", "-fuzz", match, "-fuzztime", *fuzztime, t.Package)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(stderr, "fuzzall: fuzzing %s in %s: %v\n", t.Name, t.Package, err)
			return exitFailed
		}
	}
	return exitOK
}
