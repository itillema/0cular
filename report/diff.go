package report

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/itillema/0cular/diff"
)

// wholeDocument is how reports show the JSON Pointer "", which points to
// the whole document.
const wholeDocument = "(whole document)"

// writeDiffText writes r for a terminal:
//
//	0cular diff  bot-features  baseline config/bot-features.json sha256:aa34cd56ef78…  candidate config/bot-features.json sha256:bb12cd34ef56…
//	  ! budget     feature-count-stable  120 → 240 (+100%); budget ±20% (needs approval)
//	  ✓ envelope   baseline              921 B ≤ 5 MiB; valid JSON
//	  ✓ envelope   candidate             1.8 KiB ≤ 5 MiB; valid JSON
//	120 changes: 120 added
//	  add      /features/-  {"name": "feature-121", "weight": 0.5}
//	  and 70 more
//
// Every string from the result is shown with visible escapes, so each check
// and each change takes one line.
func writeDiffText(w io.Writer, r *diff.Result) error {
	var b strings.Builder
	b.WriteString("0cular diff  " + textField(r.Contract, maxNameRunes))
	b.WriteString("  baseline " + textArtifact(r.Baseline))
	b.WriteString("  candidate " + textArtifact(r.Candidate) + "\n")
	writeTextChecks(&b, r.Checks)
	b.WriteString(describeChanges(r) + "\n")
	width := 0
	for _, c := range r.Changes {
		width = max(width, min(utf8.RuneCountInString(textPath(c.Path)), maxNameColumn))
	}
	for _, c := range r.Changes {
		path := textPath(c.Path)
		fmt.Fprintf(&b, "  %-7s  %s%s  %s\n", textField(string(c.Op), maxNameRunes), path, pad(path, width), textField(changeValues(c), maxDetailRunes))
	}
	if more := r.Count() - len(r.Changes); more > 0 {
		fmt.Fprintf(&b, "  and %d more\n", more)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// writeDiffMarkdown writes r for a GitHub Job Summary or pull request
// comment: a heading with the number of changes, the versions compared, a
// table of the checks, and a table of the changes. Every string from the
// result goes through markdownText.
func writeDiffMarkdown(w io.Writer, r *diff.Result) error {
	var b strings.Builder
	heading := "❌ not compared"
	if r.Compared {
		heading = countChanges(r.Count())
	}
	fmt.Fprintf(&b, "### 0cular diff: %s\n\n", heading)
	b.WriteString("Contract " + markdownText(r.Contract, maxMarkdownName))
	b.WriteString(" · baseline " + markdownArtifact(r.Baseline))
	b.WriteString(" · candidate " + markdownArtifact(r.Candidate) + "\n\n")
	writeMarkdownChecks(&b, r.Checks)
	if r.Compared && r.Count() > 0 {
		b.WriteString("\n" + describeChanges(r) + "\n\n| Op | Path | Old | New |\n| --- | --- | --- | --- |\n")
		for _, c := range r.Changes {
			path := wholeDocument
			if c.Path != "" {
				path = c.Path
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", markdownText(string(c.Op), maxMarkdownName),
				markdownText(path, maxMarkdownKey), markdownText(c.Old, maxMarkdownDetail), markdownText(c.New, maxMarkdownDetail))
		}
		if more := r.Count() - len(r.Changes); more > 0 {
			fmt.Fprintf(&b, "\nAnd %d more.\n", more)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// textPath shows a change's JSON Pointer.
func textPath(pointer string) string {
	if pointer == "" {
		return wholeDocument
	}
	return textField(pointer, maxKeyRunes)
}

// changeValues is what a change's line shows after its path: the value an
// add puts in, the value a remove takes out, or both for a replace.
func changeValues(c diff.Change) string {
	switch {
	case c.Old == "":
		return c.New
	case c.New == "":
		return c.Old
	}
	return c.Old + " → " + c.New
}

// describeChanges sums up a result's changes, as in "5 changes: 2 added, 2
// removed and 1 replaced".
func describeChanges(r *diff.Result) string {
	switch {
	case !r.Compared:
		return "Not compared, since a version doesn't decode."
	case r.Count() == 0:
		return "No changes."
	}
	var parts []string
	for _, kind := range []struct {
		n    int
		verb string
	}{{r.Added, "added"}, {r.Removed, "removed"}, {r.Replaced, "replaced"}} {
		if kind.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", kind.n, kind.verb))
		}
	}
	list := parts[0]
	if len(parts) > 1 {
		list = strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
	return countChanges(r.Count()) + ": " + list
}

// countChanges says how many changes there are: "no changes", "1 change"
// or "5 changes".
func countChanges(n int) string {
	switch n {
	case 0:
		return "no changes"
	case 1:
		return "1 change"
	}
	return fmt.Sprintf("%d changes", n)
}

// The JSON format of a diff, version 1. Every field is always present,
// except a change's old and new values, which an add and a remove leave
// out.
type (
	jsonDiff struct {
		SchemaVersion int          `json:"schemaVersion"`
		Contract      jsonContract `json:"contract"`
		Baseline      jsonArtifact `json:"baseline"`
		Candidate     jsonArtifact `json:"candidate"`
		Checks        []jsonCheck  `json:"checks"`
		Compared      bool         `json:"compared"`
		Changes       jsonChanges  `json:"changes"`
	}
	// jsonChanges counts the patch's operations, and lists the first ones.
	jsonChanges struct {
		Count      int          `json:"count"`
		Added      int          `json:"added"`
		Removed    int          `json:"removed"`
		Replaced   int          `json:"replaced"`
		Operations []jsonChange `json:"operations"`
	}
	jsonChange struct {
		Op   diff.Op `json:"op"`
		Path string  `json:"path"`
		Old  string  `json:"old,omitempty"`
		New  string  `json:"new,omitempty"`
	}
)

func toJSONDiff(r *diff.Result) jsonDiff {
	out := jsonDiff{
		SchemaVersion: SchemaVersion,
		Contract:      jsonContract{Name: r.Contract, Digest: r.ContractDigest},
		Baseline:      jsonArtifact(r.Baseline),
		Candidate:     jsonArtifact(r.Candidate),
		Checks:        toJSONChecks(r.Checks),
		Compared:      r.Compared,
		Changes: jsonChanges{
			Count: r.Count(), Added: r.Added, Removed: r.Removed, Replaced: r.Replaced,
			Operations: make([]jsonChange, 0, len(r.Changes)),
		},
	}
	for _, c := range r.Changes {
		out.Changes.Operations = append(out.Changes.Operations, jsonChange(c))
	}
	return out
}
