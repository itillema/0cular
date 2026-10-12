package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// run executes the command line in args, writing to stdout and stderr, and
// returns the process exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	root := newRootCommand(stdout, stderr)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return exitPass
	}
	var ee *exitError
	if !errors.As(err, &ee) {
		// Command bodies tag every error they return (see tagCommandErrors), so
		// an untagged error comes from cobra itself: the command line was wrong.
		fmt.Fprintf(stderr, "0cular: %v\nRun '0cular --help' for usage.\n", err)
		return exitUsage
	}
	if ee.err != nil {
		fmt.Fprintf(stderr, "0cular: %v\n", ee.err)
	}
	return ee.code
}

// newRootCommand builds the 0cular command tree, writing to stdout and stderr.
func newRootCommand(stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:   "0cular",
		Short: "Check, keep and roll out config and data changes safely",
		Long: "0cular gives config and data changes the safety net that code already has: it checks a change\n" +
			"before it's published, keeps the last known good copy when loading, and rolls changes out in\n" +
			"rings with automatic revert.",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE:          showUsage,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.AddCommand(newVersionCommand(), newContractCommand())
	tagCommandErrors(root)
	return root
}

// showUsage is the RunE of a command that only groups other commands. Run on
// its own, there's nothing for it to do, so it shows the usage and fails.
func showUsage(cmd *cobra.Command, _ []string) error {
	fmt.Fprint(cmd.ErrOrStderr(), cmd.UsageString())
	return &exitError{code: exitUsage}
}

// tagCommandErrors wraps the RunE of cmd and of every subcommand so that each
// error a command body returns carries an exit code: its own, or exitInternal.
func tagCommandErrors(cmd *cobra.Command) {
	if body := cmd.RunE; body != nil {
		cmd.RunE = func(c *cobra.Command, args []string) error {
			err := body(c, args)
			var ee *exitError
			if err == nil || errors.As(err, &ee) {
				return err
			}
			return &exitError{code: exitInternal, err: err}
		}
	}
	for _, sub := range cmd.Commands() {
		tagCommandErrors(sub)
	}
}
