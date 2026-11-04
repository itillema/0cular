package report

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/itillema/0cular/diff"
	"github.com/itillema/0cular/eval"
)

// Format is a report format.
type Format string

// The formats Write and WriteDiff render.
const (
	// Text is for terminals: a check's verdict and approvals, or a diff's
	// versions, then a line for each check, with the checks that need
	// attention first, and a line for each change a diff lists.
	Text Format = "text"
	// Markdown is for GitHub Job Summaries and pull request comments: the
	// verdict and a list of approvals, or a diff's number of changes, then a
	// table of the checks, attention first, and a table of the changes a
	// diff lists.
	Markdown Format = "md"
	// JSON is for programs: every field of the result, in step order. Its
	// schemaVersion is 1.
	JSON Format = "json"
)

// SchemaVersion is the version of the JSON format. A change that could
// break a reader raises it.
const SchemaVersion = 1

// ErrFormat is the error ParseFormat, Write and WriteDiff return for a
// format they don't know.
var ErrFormat = errors.New("unknown report format")

// Formats returns the formats Write and WriteDiff render.
func Formats() []Format {
	return []Format{Text, Markdown, JSON}
}

// ParseFormat returns the format that name names: text, md or json.
// markdown names Markdown too.
func ParseFormat(name string) (Format, error) {
	if name == "markdown" {
		return Markdown, nil
	}
	if f := Format(name); slices.Contains(Formats(), f) {
		return f, nil
	}
	return "", unknownFormat(name)
}

// Write renders a check's result to w in format f.
func Write(w io.Writer, r *eval.Result, f Format) error {
	switch f {
	case Text:
		return writeText(w, r)
	case Markdown:
		return writeMarkdown(w, r)
	case JSON:
		return writeJSON(w, toJSON(r))
	}
	return unknownFormat(string(f))
}

// WriteDiff renders a diff's result to w in format f: in text and
// Markdown, the diff budgets and envelopes, then the changes; in JSON,
// every field of the result.
func WriteDiff(w io.Writer, r *diff.Result, f Format) error {
	switch f {
	case Text:
		return writeDiffText(w, r)
	case Markdown:
		return writeDiffMarkdown(w, r)
	case JSON:
		return writeJSON(w, toJSONDiff(r))
	}
	return unknownFormat(string(f))
}

// unknownFormat is the error for a format named name that isn't one.
func unknownFormat(name string) error {
	return fmt.Errorf("%w %q: use text, md or json", ErrFormat, visible(cut(name, 40)))
}

// byAttention orders checks for a reader: those that block first, then
// those that need approval or were approved, then the skipped ones, then
// those that passed, in step order within each.
func byAttention(checks []eval.Check) []eval.Check {
	rank := func(s eval.Status) int {
		switch s {
		case eval.StatusFail, eval.StatusError:
			return 0
		case eval.StatusNeedsApproval, eval.StatusApproved:
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

// expiry formats when an approval expires, in UTC.
func expiry(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// hasApprover reports whether an approval names who approved.
func hasApprover(a eval.ApprovalOutcome) bool {
	return strings.TrimSpace(a.By) != ""
}
