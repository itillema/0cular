package main

import (
	"github.com/spf13/cobra"

	"github.com/itillema/0cular/contract"
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
	cmd.AddCommand(newContractSchemaCommand())
	return cmd
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
