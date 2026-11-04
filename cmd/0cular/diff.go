package main

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/itillema/0cular/contract"
	"github.com/itillema/0cular/diff"
	"github.com/itillema/0cular/report"
)

// newDiffCommand builds the diff command.
func newDiffCommand() *cobra.Command {
	var contractFile, baselineFile, candidateFile, format string
	cmd := &cobra.Command{
		Use:   "diff --contract FILE --baseline FILE --candidate FILE",
		Short: "Show what changed between two versions of an artifact",
		Long: "Compare two versions of an artifact under its contract: decode both under the contract's caps,\n" +
			"measure the change in each diff budget, and list the changes as a summary of the JSON Patch\n" +
			"(RFC 6902) that turns the baseline into the candidate. Values in fields that hold secrets show\n" +
			"[redacted]. The report goes to standard output.\n\n" +
			"diff doesn't gate the candidate; check does.\n\n" +
			"Exit codes: 0 when the versions were compared, 1 when one doesn't decode under the contract,\n" +
			"64 for a usage or contract error, and 70 for an internal error.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := report.ParseFormat(format)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			r, err := compareVersions(cmd.Context(), contractFile, baselineFile, candidateFile)
			if err != nil {
				return err
			}
			if err := report.WriteDiff(cmd.OutOrStdout(), r, f); err != nil {
				return err
			}
			if !r.Compared {
				return &exitError{code: exitBlock}
			}
			return nil
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&contractFile, "contract", "", "the contract `file`")
	flags.StringVar(&baselineFile, "baseline", "", "the baseline version's `file`")
	flags.StringVar(&candidateFile, "candidate", "", "the candidate version's `file`")
	flags.StringVar(&format, "format", "text", "the report's `format`: text, md or json")
	for _, name := range []string{"contract", "baseline", "candidate"} {
		_ = cmd.MarkFlagRequired(name)
	}
	return cmd
}

// compareVersions loads the contract and both versions, and compares them.
// A contract that doesn't load or compile, or a version that can't be
// read, is a usage error.
func compareVersions(ctx context.Context, contractFile, baselineFile, candidateFile string) (*diff.Result, error) {
	c, err := contract.LoadFile(contractFile)
	if err != nil {
		return nil, &exitError{code: exitUsage, err: err}
	}
	d, err := diff.New(c)
	var invalid *contract.Error
	switch {
	case errors.As(err, &invalid):
		return nil, &exitError{code: exitUsage, err: err}
	case err != nil:
		return nil, err
	}
	baseline, err := readVersion(ctx, "baseline", baselineFile, c.Artifact)
	if err != nil {
		return nil, err
	}
	candidate, err := readVersion(ctx, "candidate", candidateFile, c.Artifact)
	if err != nil {
		return nil, err
	}
	return d.Compare(ctx, baseline, candidate)
}
