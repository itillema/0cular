package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/itillema/0cular/contract"
	"github.com/itillema/0cular/eval"
	"github.com/itillema/0cular/gate"
	"github.com/itillema/0cular/report"
)

// approvalLifetime is how long an approval that check --approve makes
// stays valid. check applies it at once, so the expiry only goes on
// record; an approval has to have one (ADR-0016).
const approvalLifetime = time.Hour

// checkFlags are the check command's flags.
type checkFlags struct {
	contractFile, candidateFile, baselineFile, format string
	noBaseline                                        bool
	// approve asks check to approve the candidate's breached budgets, as
	// approver, for reason.
	approve          bool
	approver, reason string
}

// newCheckCommand builds the check command.
func newCheckCommand() *cobra.Command {
	var fl checkFlags
	cmd := &cobra.Command{
		Use:   "check --contract FILE --candidate FILE (--baseline FILE | --no-baseline) [--approve --approver NAME --reason TEXT]",
		Short: "Check a candidate version of an artifact against its contract",
		Long: "Check a candidate version of an artifact against its contract and a baseline version: decode it\n" +
			"under the contract's caps, validate it against the contract's JSON Schema, evaluate the\n" +
			"invariants, and measure the diff budgets against the baseline. The report goes to standard\n" +
			"output.\n\n" +
			"Name the baseline with --baseline, or check without one with --no-baseline, which skips the\n" +
			"diff budgets. One of the two is required.\n\n" +
			"A breached diff budget needs approval (NEEDS_APPROVAL), unless the contract says it can't be\n" +
			"overridden. --approve, with --approver and --reason, approves the breached budgets of this\n" +
			"check, and the verdict becomes PASS. The report records who approved and why. Nothing can\n" +
			"approve a BLOCK.\n\n" +
			"Exit codes: 0 PASS, 1 BLOCK, 3 NEEDS_APPROVAL, 64 for a usage or contract error, and 70 for\n" +
			"an internal error.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := report.ParseFormat(fl.format)
			if err != nil {
				return &exitError{code: exitUsage, err: err}
			}
			if fl.approve && (strings.TrimSpace(fl.approver) == "" || strings.TrimSpace(fl.reason) == "") {
				return &exitError{code: exitUsage, err: errors.New("--approve needs who approves, in --approver, and why, in --reason")}
			}
			r, err := check(cmd.Context(), fl)
			if err != nil {
				return err
			}
			if fl.approve && len(r.Approvals) == 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "0cular: nothing was approved: %s\n", notApproved(r.Verdict))
			}
			if err := report.Write(cmd.OutOrStdout(), r, f); err != nil {
				return err
			}
			return verdictExit(r.Verdict)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&fl.contractFile, "contract", "", "the contract `file`")
	flags.StringVar(&fl.candidateFile, "candidate", "", "the candidate version's `file`")
	flags.StringVar(&fl.baselineFile, "baseline", "", "the baseline version's `file`, which the diff budgets measure change from")
	flags.BoolVar(&fl.noBaseline, "no-baseline", false, "check without a baseline, skipping the diff budgets")
	flags.StringVar(&fl.format, "format", "text", "the report's `format`: text, md or json")
	flags.BoolVar(&fl.approve, "approve", false, "approve the diff budgets that the candidate breaches")
	flags.StringVar(&fl.approver, "approver", "", "the `name` of who approves, with --approve")
	flags.StringVar(&fl.reason, "reason", "", "why, as `text`, with --approve")
	_ = cmd.MarkFlagRequired("contract")
	_ = cmd.MarkFlagRequired("candidate")
	cmd.MarkFlagsOneRequired("baseline", "no-baseline")
	cmd.MarkFlagsMutuallyExclusive("baseline", "no-baseline")
	cmd.MarkFlagsRequiredTogether("approve", "approver", "reason")
	return cmd
}

// check loads the contract and the versions, and checks the candidate.
// With --approve, when the candidate needs approval, it approves what the
// check measured and checks again with the approval. A contract that
// doesn't load or compile, or a version that can't be read, is a usage
// error.
func check(ctx context.Context, fl checkFlags) (*eval.Result, error) {
	c, err := contract.LoadFile(fl.contractFile)
	if err != nil {
		return nil, &exitError{code: exitUsage, err: err}
	}
	candidate, err := readVersion(ctx, "candidate", fl.candidateFile, c.Artifact)
	if err != nil {
		return nil, err
	}
	var baseline *eval.Input
	if !fl.noBaseline {
		b, err := readVersion(ctx, "baseline", fl.baselineFile, c.Artifact)
		if err != nil {
			return nil, err
		}
		baseline = &b
	}
	r, err := gate.Check(ctx, c, candidate, baseline)
	var invalid *contract.Error
	switch {
	case errors.As(err, &invalid):
		return nil, &exitError{code: exitUsage, err: err}
	case err != nil:
		return nil, err
	case !fl.approve || r.Verdict != eval.NeedsApproval:
		return r, nil
	}
	a, err := gate.Approve(r, fl.approver, fl.reason, time.Now().Add(approvalLifetime))
	if err != nil {
		return nil, err
	}
	return gate.Check(ctx, c, candidate, baseline, gate.WithApproval(a))
}

// notApproved says why --approve approved nothing, given the verdict.
func notApproved(v eval.Verdict) string {
	if v == eval.Block {
		return "the candidate is blocked, which no approval can accept"
	}
	return "nothing needs approval"
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
