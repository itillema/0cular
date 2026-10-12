package report

import (
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/itillema/0cular/eval"
)

// Format is a report format.
type Format string

// The formats Write renders.
const (
	// Text is for terminals: the verdict, then a line for each check, with
	// the checks that need attention first.
	Text Format = "text"
	// Markdown is for GitHub Job Summaries and pull request comments: the
	// verdict, then a table of the checks, attention first.
	Markdown Format = "markdown"
	// JSON is for programs: every field of the result, in step order. Its
	// schemaVersion is 1.
	JSON Format = "json"
)

// SchemaVersion is the version of the JSON format. A change that could
// break a reader raises it.
const SchemaVersion = 1

// ErrFormat is the error ParseFormat and Write return for a format they
// don't know.
var ErrFormat = errors.New("unknown report format")

// Formats returns the formats Write renders.
func Formats() []Format {
	return []Format{Text, Markdown, JSON}
}

// ParseFormat returns the format that name names, such as markdown.
func ParseFormat(name string) (Format, error) {
	if f := Format(name); slices.Contains(Formats(), f) {
		return f, nil
	}
	return "", fmt.Errorf("%w %q: use text, markdown or json", ErrFormat, visible(cut(name, 40)))
}

// Write renders r to w in format f.
func Write(w io.Writer, r *eval.Result, f Format) error {
	switch f {
	case Text:
		return writeText(w, r)
	case Markdown:
		return writeMarkdown(w, r)
	case JSON:
		return writeJSON(w, r)
	}
	return fmt.Errorf("%w %q: use text, markdown or json", ErrFormat, visible(cut(string(f), 40)))
}

// byAttention orders checks for a reader: those that block first, then
// those that need approval, then the skipped ones, then those that passed,
// in step order within each.
func byAttention(checks []eval.Check) []eval.Check {
	rank := func(s eval.Status) int {
		switch s {
		case eval.StatusFail, eval.StatusError:
			return 0
		case eval.StatusNeedsApproval:
			return 1
		case eval.StatusSkipped:
			return 2
		}
		return 3
	}
	sorted := slices.Clone(checks)
	slices.SortStableFunc(sorted, func(a, b eval.Check) int { return rank(a.Status) - rank(b.Status) })
	return sorted
}

// shortDigest shortens a "sha256:" digest to its first 12 hex digits.
func shortDigest(digest string) string {
	return cut(digest, len("sha256:")+13)
}
