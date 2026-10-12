package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/itillema/0cular/eval"
)

// The JSON format, version 1. Every field is always present, except the
// optional parts of a check, and the order of checks is the result's: by
// step, then as the contract lists them.
type (
	jsonReport struct {
		SchemaVersion int           `json:"schemaVersion"`
		Verdict       eval.Verdict  `json:"verdict"`
		Contract      jsonContract  `json:"contract"`
		Candidate     jsonArtifact  `json:"candidate"`
		Baseline      *jsonArtifact `json:"baseline"`
		Checks        []jsonCheck   `json:"checks"`
	}
	jsonContract struct {
		Name   string `json:"name"`
		Digest string `json:"digest"`
	}
	jsonArtifact struct {
		Key    string `json:"key"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
	}
	jsonCheck struct {
		Kind           eval.Kind       `json:"kind"`
		Name           string          `json:"name"`
		Status         eval.Status     `json:"status"`
		Message        string          `json:"message"`
		Evidence       string          `json:"evidence"`
		Violations     []jsonViolation `json:"violations,omitempty"`
		ViolationCount int             `json:"violationCount,omitempty"`
		Budget         *jsonBudget     `json:"budget,omitempty"`
		Cost           uint64          `json:"cost"`
		DurationMs     float64         `json:"durationMs"`
	}
	jsonViolation struct {
		Pointer string `json:"pointer"`
		Keyword string `json:"keyword"`
		Message string `json:"message"`
	}
	// jsonBudget holds a measure budget's values, or a collection budget's
	// counts. A redacted measure has no values (ADR-0015).
	jsonBudget struct {
		Kind           string   `json:"kind"`
		Baseline       *float64 `json:"baseline,omitempty"`
		Candidate      *float64 `json:"candidate,omitempty"`
		Redacted       bool     `json:"redacted,omitempty"`
		BaselineItems  *int     `json:"baselineItems,omitempty"`
		CandidateItems *int     `json:"candidateItems,omitempty"`
		Removed        *int     `json:"removed,omitempty"`
		Added          *int     `json:"added,omitempty"`
		Changed        *int     `json:"changed,omitempty"`
		Breached       []string `json:"breached"`
	}
)

// writeJSON writes r as indented JSON, schemaVersion 1. HTML's < > & and
// every character that isn't shown as itself are \u escapes, so the bytes
// are safe to show in a terminal and to embed in HTML.
func writeJSON(w io.Writer, r *eval.Result) error {
	data, err := json.MarshalIndent(toJSON(r), "", "  ")
	if err != nil {
		return fmt.Errorf("report: %w", err)
	}
	_, err = w.Write(append(escapeInvisible(data), '\n'))
	return err
}

func toJSON(r *eval.Result) jsonReport {
	out := jsonReport{
		SchemaVersion: SchemaVersion,
		Verdict:       r.Verdict,
		Contract:      jsonContract{Name: r.Contract, Digest: r.ContractDigest},
		Candidate:     jsonArtifact(r.Candidate),
		Checks:        make([]jsonCheck, 0, len(r.Checks)),
	}
	if r.Baseline != nil {
		b := jsonArtifact(*r.Baseline)
		out.Baseline = &b
	}
	for _, c := range r.Checks {
		jc := jsonCheck{
			Kind:           c.Kind,
			Name:           c.Name,
			Status:         c.Status,
			Message:        c.Message,
			Evidence:       c.Evidence,
			ViolationCount: c.ViolationCount,
			Cost:           c.Cost,
			DurationMs:     milliseconds(c.Duration),
		}
		for _, v := range c.Violations {
			jc.Violations = append(jc.Violations, jsonViolation(v))
		}
		if v := c.Budget; v != nil {
			jc.Budget = toJSONBudget(v)
		}
		out.Checks = append(out.Checks, jc)
	}
	return out
}

func toJSONBudget(v *eval.BudgetValues) *jsonBudget {
	b := &jsonBudget{Kind: "measure", Breached: append([]string{}, v.Breached...)}
	switch {
	case v.Collection:
		b.Kind = "collection"
		b.BaselineItems, b.CandidateItems = &v.BaselineItems, &v.CandidateItems
		b.Removed, b.Added, b.Changed = &v.Removed, &v.Added, &v.Changed
	case v.Redacted:
		b.Redacted = true
	default:
		b.Baseline, b.Candidate = &v.Baseline, &v.Candidate
	}
	return b
}

// milliseconds returns d in milliseconds, to the microsecond.
func milliseconds(d time.Duration) float64 {
	return math.Round(float64(d)/float64(time.Microsecond)) / 1000
}

// escapeInvisible rewrites, as \u escapes, the characters that
// encoding/json leaves as they are but that aren't shown as themselves,
// such as DEL and U+202E, and U+FFFD. JSON's syntax is ASCII, so any such character is inside
// a string, where an escape means the same character.
func escapeInvisible(data []byte) []byte {
	var b bytes.Buffer
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		switch {
		// encoding/json escapes the other ASCII controls, and the rest of
		// ASCII, line breaks included, is JSON's own syntax. Go releases
		// differ on U+FFFD, which replaces bytes that aren't UTF-8, so it's
		// always an escape.
		case r < utf8.RuneSelf && r != 0x7f, r >= utf8.RuneSelf && r != utf8.RuneError && !invisible(r):
			b.Write(data[i : i+size])
		case r > 0xffff:
			r1, r2 := utf16.EncodeRune(r)
			fmt.Fprintf(&b, `\u%04x\u%04x`, r1, r2)
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
		i += size
	}
	return b.Bytes()
}
