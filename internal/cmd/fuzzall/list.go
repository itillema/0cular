package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"unicode"
)

// target is one fuzz target: a FuzzXxx function in a package.
type target struct {
	Package string
	Name    string
}

// testEvent is the part of a go test -json event that fuzzall reads.
type testEvent struct {
	Action  string
	Package string
	Output  string
}

// listTargets runs go test -json -list for the package patterns and returns
// the fuzz targets it reports, in the order go test lists them.
func listTargets(ctx context.Context, patterns []string, stderr io.Writer) ([]target, error) {
	args := append([]string{"test", "-json", "-list", "^Fuzz"}, patterns...)
	cmd := exec.CommandContext(ctx, "go", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("listing fuzz targets: %w", err)
	}
	return parseList(&out)
}

// parseList reads the events of go test -json -list and returns the fuzz
// targets they name. go test reports each listed name as an "output" event
// of its package.
func parseList(r io.Reader) ([]target, error) {
	var targets []target
	dec := json.NewDecoder(r)
	for {
		var ev testEvent
		err := dec.Decode(&ev)
		if errors.Is(err, io.EOF) {
			return targets, nil
		}
		if err != nil {
			return nil, fmt.Errorf("parsing go test -json output: %w", err)
		}
		if ev.Action != "output" || ev.Package == "" {
			continue
		}
		if name := strings.TrimSpace(ev.Output); isFuzzName(name) {
			targets = append(targets, target{Package: ev.Package, Name: name})
		}
	}
}

// isFuzzName reports whether name is a fuzz target's name under the rule the
// testing package uses: "Fuzz", optionally followed by an identifier that
// doesn't start with a lowercase letter.
func isFuzzName(name string) bool {
	rest, ok := strings.CutPrefix(name, "Fuzz")
	if !ok {
		return false
	}
	for i, r := range rest {
		if i == 0 && unicode.IsLower(r) {
			return false
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
