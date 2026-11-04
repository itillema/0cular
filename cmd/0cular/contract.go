package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/itillema/0cular/contract"
	"github.com/itillema/0cular/eval"
)

// newContractCommand builds the contract command, which groups the commands
// for contract files.
func newContractCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "contract",
		Short: "Work with contract files",
		Args:  cobra.NoArgs,
		RunE:  showUsage,
	}
	cmd.AddCommand(newContractLintCommand(), newContractSchemaCommand())
	return cmd
}

// newContractLintCommand builds the contract lint command.
func newContractLintCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "lint FILE...",
		Short: "Check contract files for mistakes",
		Long: "Check contract files for mistakes: everything that stops a contract from loading, and also\n" +
			"  - a rule that could go over its cost limit on an artifact the contract allows;\n" +
			"  - a format in the JSON Schema that 0cular doesn't check;\n" +
			"  - a budget that reads baseline, which is null when it runs;\n" +
			"  - arithmetic that mixes an int with a double, which fails when it runs (a warning).\n\n" +
			"Each finding is printed as file:line:column: severity: field: message. The command exits 0\n" +
			"when no file has an error, even with warnings; 1 when one does; and 64 when a file can't\n" +
			"be read.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, files []string) error {
			return lintFiles(cmd.OutOrStdout(), cmd.ErrOrStderr(), files)
		},
	}
}

// lintFiles lints each file and prints its findings. It returns an
// *exitError with exitBlock if a file has an error, or exitUsage if a file
// can't be read.
func lintFiles(stdout, stderr io.Writer, files []string) error {
	code := exitPass
	for _, file := range files {
		findings, err := lintFile(file)
		var unreadable *exitError
		switch {
		case errors.As(err, &unreadable):
			fmt.Fprintf(stderr, "0cular: %v\n", unreadable.err)
			code = max(code, unreadable.code)
			continue
		case err != nil:
			return err
		}
		for _, f := range findings {
			sep := ": "
			if f.Pos.IsValid() {
				sep = ":"
			}
			if _, err := fmt.Fprintln(stdout, file+sep+f.String()); err != nil {
				return err
			}
			if f.Severity == eval.SeverityError {
				code = max(code, exitBlock)
			}
		}
	}
	if code != exitPass {
		return &exitError{code: code}
	}
	return nil
}

// lintFile loads and lints one contract file. A contract that doesn't load
// is a list of error findings; a file that can't be read returns an
// *exitError with exitUsage.
func lintFile(file string) ([]eval.Finding, error) {
	c, err := contract.LoadFile(file)
	var invalid *contract.Error
	if errors.As(err, &invalid) {
		findings := make([]eval.Finding, len(invalid.Problems))
		for i, p := range invalid.Problems {
			findings[i] = eval.Finding{Problem: p, Severity: eval.SeverityError}
		}
		return findings, nil
	}
	if err != nil {
		return nil, &exitError{code: exitUsage, err: err}
	}
	return eval.Lint(c)
}

// newContractSchemaCommand builds the contract schema command.
func newContractSchemaCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Print the JSON Schema for contract files",
		Long: "Print the JSON Schema (Draft 2020-12) for contract files, for editors and other tools.\n" +
			"Editors can also fetch it from " + contract.JSONSchemaID + "\n\n" +
			"0cular checks a few things the schema can't, such as unique check names, so a contract\n" +
			"that matches the schema can still fail to load.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := cmd.OutOrStdout().Write(contract.JSONSchema())
			return err
		},
	}
}
