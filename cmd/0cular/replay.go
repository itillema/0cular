package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/itillema/0cular/eval"
	"github.com/itillema/0cular/replay"
	"github.com/itillema/0cular/report"
)

// newReplayCommand builds the replay command, which groups the commands for
// the replay suite.
func newReplayCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "replay",
		Short: "Run the replay scenarios",
		Args:  cobra.NoArgs,
		RunE:  showUsage,
	}
	cmd.AddCommand(newReplayRunCommand())
	return cmd
}

// newReplayRunCommand builds the replay run command.
func newReplayRunCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "run [SCENARIO...]",
		Short: "Run the replay scenarios built into 0cular",
		Long: "Run the replay scenarios built into 0cular, or the ones named. Each reconstructs the shape of a\n" +
			"public outage with synthetic data. For each one, the command checks the candidate against the\n" +
			"baseline under the scenario's contract, prints the report, and says whether the outcome is the\n" +
			"one the scenario expects.\n\n" +
			"Exits 0 when every scenario goes as expected, 1 when one doesn't, and 64 for an unknown\n" +
			"scenario.",
		RunE: func(cmd *cobra.Command, names []string) error {
			all, err := replay.Scenarios()
			if err != nil {
				return err
			}
			return replayRun(cmd.Context(), cmd.OutOrStdout(), all, names)
		},
	}
}

// replayRun runs the scenarios in all that names names, or every one if
// names is empty, and writes each one's report and outcome to w.
func replayRun(ctx context.Context, w io.Writer, all []replay.Scenario, names []string) error {
	scenarios, err := selectScenarios(all, names)
	if err != nil {
		return err
	}
	var b strings.Builder
	unexpected := 0
	for _, s := range scenarios {
		fmt.Fprintf(&b, "%s: %s\n%s\n\n", s.Name, s.Expected.Postmortem.Title, s.Expected.Postmortem.URL)
		r, err := s.Check(ctx)
		if err != nil {
			return err
		}
		if err := report.Write(&b, r, report.Text); err != nil {
			return err
		}
		if problems := s.Unexpected(r); len(problems) > 0 {
			unexpected++
			b.WriteString("\nNot as expected:\n")
			for _, p := range problems {
				b.WriteString("  - " + p + "\n")
			}
		} else {
			b.WriteString("\nAs expected: " + describeStage(s.Expected.Stages.Check) + ".\n")
		}
		b.WriteString("\n")
	}
	if unexpected > 0 {
		fmt.Fprintf(&b, "%d of %d scenarios didn't go as expected.\n", unexpected, len(scenarios))
	} else {
		fmt.Fprintf(&b, "%d of %d scenarios went as expected. This version of 0cular runs the check stage; the loader,\n"+
			"publish and rings stages come in later versions.\n", len(scenarios), len(scenarios))
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return err
	}
	if unexpected > 0 {
		return &exitError{code: exitBlock}
	}
	return nil
}

// selectScenarios returns the scenarios named, in the order named, or all
// of them if names is empty. An unknown name is a usage error.
func selectScenarios(all []replay.Scenario, names []string) ([]replay.Scenario, error) {
	if len(names) == 0 {
		return all, nil
	}
	known := make([]string, len(all))
	for i, s := range all {
		known[i] = s.Name
	}
	var selected []replay.Scenario
	for _, name := range names {
		i := slices.Index(known, name)
		if i < 0 {
			return nil, &exitError{code: exitUsage, err: fmt.Errorf("there's no scenario %q; the scenarios are %s", name, strings.Join(known, ", "))}
		}
		selected = append(selected, all[i])
	}
	return selected, nil
}

// describeStage says what a check stage expects, as in "BLOCK, with
// feature-cap failing and feature-count-stable needing approval".
func describeStage(stage replay.CheckStage) string {
	var parts []string
	for _, c := range stage.Checks {
		switch c.Status {
		case eval.StatusFail:
			parts = append(parts, c.Name+" failing")
		case eval.StatusNeedsApproval:
			parts = append(parts, c.Name+" needing approval")
		default:
			parts = append(parts, c.Name+" ("+string(c.Status)+")")
		}
	}
	switch len(parts) {
	case 0:
		return string(stage.Verdict)
	case 1:
		return string(stage.Verdict) + ", with " + parts[0]
	}
	return string(stage.Verdict) + ", with " + strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}
