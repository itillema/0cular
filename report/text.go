package report

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/itillema/0cular/eval"
)

// How many characters each kind of field may show. Evidence and messages
// are already short, since eval cuts the values it quotes; these bound the
// rest, such as a file path.
const (
	maxNameRunes   = 100
	maxKeyRunes    = 200
	maxDetailRunes = 1000
	// maxNameColumn is the widest the name column grows; a longer name
	// pushes its line's evidence along.
	maxNameColumn = 40
)

// writeText writes r for a terminal:
//
//	0cular check  bot-features  candidate config/bot-features.json sha256:bb12cd34ef56…  no baseline
//	BLOCK
//	  ✗ invariant  feature-cap  size(self.features) = 240, not <= 200 — the proxy preallocates 200 features
//	  ✓ schema     bot-features.schema.json
//
// Every string from the result is shown with visible escapes, so each
// check takes one line, and schema violations one line each below it.
func writeText(w io.Writer, r *eval.Result) error {
	var b strings.Builder
	b.WriteString("0cular check  " + textField(r.Contract, maxNameRunes))
	b.WriteString("  candidate " + textArtifact(r.Candidate))
	if r.Baseline != nil {
		b.WriteString("  baseline " + textArtifact(*r.Baseline))
	} else {
		b.WriteString("  no baseline")
	}
	b.WriteString("\n" + string(r.Verdict) + "\n")
	checks := byAttention(r.Checks)
	width := 0
	for _, c := range checks {
		width = max(width, min(utf8.RuneCountInString(textField(c.Name, maxNameRunes)), maxNameColumn))
	}
	for _, c := range checks {
		name := textField(c.Name, maxNameRunes)
		fmt.Fprintf(&b, "  %s %-9s  %s", textSymbol(c.Status), c.Kind, name)
		if detail := textDetail(c); detail != "" {
			b.WriteString(strings.Repeat(" ", max(width-utf8.RuneCountInString(name), 0)) + "  " + detail)
		}
		b.WriteString("\n")
		for _, v := range c.Violations {
			b.WriteString("      " + textField(describeViolation(v), maxDetailRunes) + "\n")
		}
		if more := c.ViolationCount - len(c.Violations); more > 0 {
			fmt.Fprintf(&b, "      and %d more\n", more)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// textField shows untrusted text on one line, cut to n characters.
func textField(s string, n int) string {
	return visible(cut(s, n))
}

// textArtifact identifies a version: its key, if it has one, and digest.
func textArtifact(a eval.Artifact) string {
	digest := visible(shortDigest(a.Digest))
	if a.Key == "" {
		return digest
	}
	return textField(a.Key, maxKeyRunes) + " " + digest
}

// textDetail is what a check's line shows after its name: the evidence;
// the contract's message, unless the check passed; and a note on any
// status but pass and fail.
func textDetail(c eval.Check) string {
	var parts []string
	if c.Evidence != "" {
		parts = append(parts, textField(c.Evidence, maxDetailRunes))
	}
	if c.Message != "" && c.Status != eval.StatusPass {
		parts = append(parts, "— "+textField(c.Message, maxDetailRunes))
	}
	if note := statusNote(c.Status); note != "" {
		parts = append(parts, "("+note+")")
	}
	return strings.Join(parts, " ")
}

// textSymbol marks a check's status at the start of its line.
func textSymbol(s eval.Status) string {
	switch s {
	case eval.StatusPass:
		return "✓"
	case eval.StatusNeedsApproval:
		return "!"
	case eval.StatusSkipped:
		return "-"
	}
	return "✗"
}

// statusNote names the statuses that a check's symbol doesn't make plain.
func statusNote(s eval.Status) string {
	switch s {
	case eval.StatusError:
		return "error"
	case eval.StatusNeedsApproval:
		return "needs approval"
	case eval.StatusSkipped:
		return "skipped"
	}
	return ""
}

// describeViolation says where a schema violation is and what it is.
func describeViolation(v eval.Violation) string {
	if v.Pointer == "" {
		return "at the top level: " + v.Message
	}
	return "at " + v.Pointer + ": " + v.Message
}
