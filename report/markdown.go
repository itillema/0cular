package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/itillema/0cular/eval"
)

// Markdown cuts fields shorter than text does, since a pull request comment
// is read in a narrow column, and GitHub limits a comment's size.
const (
	maxMarkdownName   = 100
	maxMarkdownKey    = 200
	maxMarkdownDetail = 500
)

// writeMarkdown writes r for a GitHub Job Summary or pull request comment:
// a heading with the verdict, the versions checked, a table of the checks
// with those that need attention first, and a list of schema violations.
// Every string from the result goes through markdownText.
func writeMarkdown(w io.Writer, r *eval.Result) error {
	var b strings.Builder
	fmt.Fprintf(&b, "### 0cular check: %s %s\n\n", verdictEmoji(r.Verdict), r.Verdict)
	b.WriteString("Contract " + markdownText(r.Contract, maxMarkdownName))
	b.WriteString(" · candidate " + markdownArtifact(r.Candidate))
	if r.Baseline != nil {
		b.WriteString(" · baseline " + markdownArtifact(*r.Baseline))
	} else {
		b.WriteString(" · no baseline")
	}
	b.WriteString("\n\n| | Kind | Check | Evidence |\n| --- | --- | --- | --- |\n")
	checks := byAttention(r.Checks)
	for _, c := range checks {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", statusEmoji(c.Status), c.Kind, markdownText(c.Name, maxMarkdownName), markdownDetail(c))
	}
	for _, c := range checks {
		if len(c.Violations) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\nSchema violations in %s:\n\n", markdownText(c.Name, maxMarkdownName))
		for _, v := range c.Violations {
			b.WriteString("- " + markdownText(describeViolation(v), maxMarkdownDetail) + "\n")
		}
		if more := c.ViolationCount - len(c.Violations); more > 0 {
			fmt.Fprintf(&b, "- and %d more\n", more)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// markdownArtifact identifies a version: its key, if it has one, and
// digest.
func markdownArtifact(a eval.Artifact) string {
	digest := markdownText(shortDigest(a.Digest), maxMarkdownKey)
	if a.Key == "" {
		return digest
	}
	return markdownText(a.Key, maxMarkdownKey) + " (" + digest + ")"
}

// markdownDetail is a check's evidence cell: the evidence; the contract's
// message, unless the check passed; and a note on any status but pass and
// fail.
func markdownDetail(c eval.Check) string {
	var parts []string
	if c.Evidence != "" {
		parts = append(parts, markdownText(c.Evidence, maxMarkdownDetail))
	}
	if c.Message != "" && c.Status != eval.StatusPass {
		parts = append(parts, "— "+markdownText(c.Message, maxMarkdownDetail))
	}
	if note := statusNote(c.Status); note != "" {
		parts = append(parts, "\\("+note+"\\)")
	}
	return strings.Join(parts, " ")
}

func verdictEmoji(v eval.Verdict) string {
	switch v {
	case eval.Pass:
		return "✅"
	case eval.NeedsApproval:
		return "⚠️"
	}
	return "❌"
}

func statusEmoji(s eval.Status) string {
	switch s {
	case eval.StatusPass:
		return "✅"
	case eval.StatusNeedsApproval:
		return "⚠️"
	case eval.StatusSkipped:
		return "⏭️"
	}
	return "❌"
}
