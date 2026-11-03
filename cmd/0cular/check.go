package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/itillema/0cular/contract"
	"github.com/itillema/0cular/eval"
	"github.com/itillema/0cular/report"
)

// newCheckCommand builds the check command.
func newCheckCommand() *cobra.Command {
	var (
		contractFile, candidateFile, baselineFile, format string
		noBaseline                                        bool
	)
	cmd := &cobra.Command{
		Use:   "check --contract FILE --candidate FILE (--baseline FILE | --no-baseline)",
		Short: "Check a candidate version of an artifact against its contract",
		Long: "Check a candidate version of an artifact against its contract and a baseline version: decode it\n" +
			"under the contract's caps, validate it against the contract's JSON Schema, evaluate the\n" +
			"invariants, and measure the diff budgets against the baseline. The report goes to standard\n" +
			"output.\n\n" +
			"Name the baseline with --baseline, or check without one with --no-baseline, which skips the\n" +
			"diff budgets. One of the two is required.\n\n" +
			"Exit codes: 0 PASS, 1 BLOCK, 3 NEEDS_APPROVAL, 64 for a usage or contract error, and 70 for\n" +
			"an internal error.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := report.ParseFormat(format)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			r, err := check(cmd.Context(), contractFile, candidateFile, baselineFile, noBaseline)
			if err != nil {
				return err
			}
			if err := report.Write(cmd.OutOrStdout(), r, f); err != nil {
				return err
			}
			return verdictExit(r.Verdict)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&contractFile, "contract", "", "the contract `file`")
	flags.StringVar(&candidateFile, "candidate", "", "the candidate version's `file`")
	flags.StringVar(&baselineFile, "baseline", "", "the baseline version's `file`, which the diff budgets measure change from")
	flags.BoolVar(&noBaseline, "no-baseline", false, "check without a baseline, skipping the diff budgets")
	flags.StringVar(&format, "format", "text", "the report's `format`: text, md or json")
	_ = cmd.MarkFlagRequired("contract")
	_ = cmd.MarkFlagRequired("candidate")
	cmd.MarkFlagsOneRequired("baseline", "no-baseline")
	cmd.MarkFlagsMutuallyExclusive("baseline", "no-baseline")
	return cmd
}

// check loads the contract and the versions, and checks the candidate. A
// contract that doesn't load or compile, or a version that can't be read,
// is a usage error.
func check(ctx context.Context, contractFile, candidateFile, baselineFile string, noBaseline bool) (*eval.Result, error) {
	c, err := contract.LoadFile(contractFile)
	if err != nil {
		return nil, &exitError{code: exitUsage, err: err}
	}
	e, err := eval.New(c)
	var invalid *contract.Error
	switch {
	case errors.As(err, &invalid):
		return nil, &exitError{code: exitUsage, err: err}
	case err != nil:
		return nil, err
	}
	candidate, err := readVersion(ctx, "candidate", candidateFile, c.Artifact)
	if err != nil {
		return nil, err
	}
	var baseline *eval.Input
	if !noBaseline {
		b, err := readVersion(ctx, "baseline", baselineFile, c.Artifact)
		if err != nil {
			return nil, err
		}
		baseline = &b
	}
	return e.Check(ctx, candidate, baseline)
}

// readVersion reads the version in file, which plays role, such as
// candidate. Its key, which rules read as meta.key, is the path as given,
// with forward slashes. A file that can't be opened is a usage error.
func readVersion(ctx context.Context, role, file string, a contract.Artifact) (eval.Input, error) {
	f, err := os.Open(file)
	if err != nil {
		return eval.Input{}, &exitError{code: exitUsage, err: fmt.Errorf("%s: %w", role, err)}
	}
	defer f.Close()
	in, err := eval.ReadInput(ctx, f, filepath.ToSlash(file), a)
	if err != nil {
		return eval.Input{}, fmt.Errorf("%s: %w", role, err)
	}
	return in, nil
}

// verdictExit returns the exit error for a verdict: none for PASS.
func verdictExit(v eval.Verdict) error {
	switch v {
	case eval.Pass:
		return nil
	case eval.NeedsApproval:
		return &exitError{code: exitNeedsApproval}
	}
	return &exitError{code: exitBlock}
}
