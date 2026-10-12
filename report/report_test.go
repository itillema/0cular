package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"github.com/itillema/0cular/eval"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// Characters that reorder or hide text, written as code points so that
// none of them appears raw in this file.
var (
	rlo  = string(rune(0x202e))  // right-to-left override
	pdf  = string(rune(0x202c))  // pop directional formatting
	lri  = string(rune(0x2066))  // left-to-right isolate
	pdi  = string(rune(0x2069))  // pop directional isolate
	zwsp = string(rune(0x200b))  // zero-width space
	nel  = string(rune(0x0085))  // next line, a C1 control
	ls   = string(rune(0x2028))  // line separator
	tag  = string(rune(0xe0041)) // tag latin capital letter A, outside the BMP
)

// hostile are strings that try to break out of every format: HTML,
// Markdown, mentions and references, line breaks, terminal escapes,
// characters that reorder or hide text, long text, and bytes that aren't
// UTF-8 (§3.12).
var hostile = []string{
	`<script>alert(1)</script> <img src=x onerror=alert(1)>`,
	"`code` and ``` fences ``` and ~~~",
	`a | b || c \| d`,
	"@octocat, @org/team, #123, owner/repo#45 and someone@example.com",
	"line one\nline two\r\n\ttabbed",
	"\x1b[31mred\x1b[0m, NUL \x00, DEL \x7f, NEL " + nel + " and LS " + ls,
	rlo + "evil" + pdf + ", " + lri + "isolate" + pdi + ", zero" + zwsp + "width, tag " + tag,
	"**bold** _em_ [link](https://example.com) ![img](https://example.com/x.png) # heading > quote - item $math$ ~~strike~~ &lt;entity&gt; &#64;",
	"bad \xff byte and \xe2\x80 truncated",
	strings.Repeat("long ", 400),
	"✓ 中文 👍 ok",
}

// blockResult is a typical result: one check of each status, from every
// step.
func blockResult() *eval.Result {
	return &eval.Result{
		Verdict:        eval.Block,
		Contract:       "bot-features",
		ContractDigest: "sha256:c0ffee0000000000000000000000000000000000000000000000000000000000",
		Candidate:      eval.Artifact{Key: "config/bot-features.json", Size: 1843, Digest: "sha256:bb12cd34ef56000000000000000000000000000000000000000000000000000"},
		Baseline:       &eval.Artifact{Key: "config/bot-features.json", Size: 921, Digest: "sha256:aa34000000000000000000000000000000000000000000000000000000000000"},
		Checks: []eval.Check{
			{Kind: eval.KindEnvelope, Name: "candidate", Status: eval.StatusPass, Evidence: "1.8 KiB ≤ 5 MiB; valid JSON", Duration: 1250 * time.Microsecond},
			{Kind: eval.KindEnvelope, Name: "baseline", Status: eval.StatusPass, Evidence: "921 B ≤ 5 MiB; valid JSON", Duration: 800 * time.Microsecond},
			{
				Kind: eval.KindSchema, Name: "bot-features.schema.json", Status: eval.StatusFail,
				Evidence:       "at /features/3/name: does not match pattern '^feature-[0-9]+$'; and 2 more",
				Violations:     []eval.Violation{{Pointer: "/features/3/name", Keyword: "/properties/features/items/properties/name/pattern", Message: "does not match pattern '^feature-[0-9]+$'"}, {Pointer: "/features/7", Keyword: "/properties/features/items/required", Message: "missing property 'weight'"}},
				ViolationCount: 3, Duration: 2 * time.Millisecond,
			},
			{Kind: eval.KindInvariant, Name: "feature-cap", Status: eval.StatusFail, Message: "the proxy preallocates 200 features", Evidence: "size(self.features) = 240, not <= 200", Cost: 3, Duration: 50 * time.Microsecond},
			{Kind: eval.KindInvariant, Name: "every-feature-named", Status: eval.StatusPass, Cost: 1201},
			{Kind: eval.KindInvariant, Name: "unique-names", Status: eval.StatusError, Evidence: "the expression exceeded its cost limit of 1000000"},
			{
				Kind: eval.KindBudget, Name: "feature-count-stable", Status: eval.StatusNeedsApproval, Evidence: "120 → 240 (+100%); budget ±20%",
				Budget: &eval.BudgetValues{Baseline: 120, Candidate: 240, Breached: []string{"maxChangePct"}}, Cost: 4,
			},
			{
				Kind: eval.KindBudget, Name: "feature-churn", Status: eval.StatusPass, Evidence: "120 → 240 items: 0 removed, 120 added, 0 changed (0%); budget at most 5 removed",
				Budget: &eval.BudgetValues{Collection: true, BaselineItems: 120, CandidateItems: 240, Added: 120}, Cost: 1440,
			},
			{
				Kind: eval.KindBudget, Name: "pin-stable", Status: eval.StatusPass, Evidence: "[redacted] → [redacted]; budget ±1%",
				Budget: &eval.BudgetValues{Baseline: 1234, Candidate: 1234, Redacted: true}, Cost: 2,
			},
		},
	}
}

// passResult is a result without a baseline, whose checks pass or are
// skipped.
func passResult() *eval.Result {
	return &eval.Result{
		Verdict:        eval.Pass,
		Contract:       "quota-policies",
		ContractDigest: "sha256:c0ffee0000000000000000000000000000000000000000000000000000000000",
		Candidate:      eval.Artifact{Size: 512, Digest: "sha256:dd56000000000000000000000000000000000000000000000000000000000000"},
		Checks: []eval.Check{
			{Kind: eval.KindEnvelope, Name: "candidate", Status: eval.StatusPass, Evidence: "512 B ≤ 1 MiB; valid YAML"},
			{Kind: eval.KindInvariant, Name: "every-policy-has-a-limit", Status: eval.StatusPass, Cost: 40},
			{Kind: eval.KindBudget, Name: "policy-count-stable", Status: eval.StatusSkipped, Evidence: "there's no baseline to compare with"},
		},
	}
}

// resultWith returns a result with s in every string field a report shows.
func resultWith(s string) *eval.Result {
	return &eval.Result{
		Verdict:        eval.Block,
		Contract:       s,
		ContractDigest: "sha256:" + s,
		Candidate:      eval.Artifact{Key: s, Size: 1, Digest: "sha256:" + s},
		Baseline:       &eval.Artifact{Key: s, Size: 2, Digest: "sha256:" + s},
		Checks: []eval.Check{
			{
				Kind: eval.KindSchema, Name: s, Status: eval.StatusFail, Message: s, Evidence: s,
				Violations:     []eval.Violation{{Pointer: s, Keyword: s, Message: s}, {Message: s}},
				ViolationCount: 5,
			},
			{Kind: eval.KindInvariant, Name: s, Status: eval.StatusError, Message: s, Evidence: s},
			{Kind: eval.KindBudget, Name: s, Status: eval.StatusNeedsApproval, Message: s, Evidence: s, Budget: &eval.BudgetValues{Breached: []string{s}}},
			{Kind: eval.KindInvariant, Name: s, Status: eval.StatusPass, Message: s, Evidence: s},
		},
	}
}

// hostileResult puts each hostile string in a field of its own.
func hostileResult() *eval.Result {
	h := func(i int) string { return hostile[i%len(hostile)] }
	r := resultWith(h(0))
	r.Candidate.Key, r.Baseline.Key = h(1), h(2)
	for i := range r.Checks {
		c := &r.Checks[i]
		c.Name, c.Message, c.Evidence = h(3*i+3), h(3*i+4), h(3*i+5)
	}
	r.Checks[0].Violations = []eval.Violation{{Pointer: h(4), Message: h(5)}, {Pointer: h(6), Message: h(7)}, {Pointer: h(8), Message: h(9)}}
	return r
}

func extension(f Format) string {
	return map[Format]string{Text: "txt", Markdown: "md", JSON: "json"}[f]
}

// TestGolden compares each format's rendering of a few results with the
// golden files in testdata. Run go test ./report -update to rewrite them,
// then review the diff.
func TestGolden(t *testing.T) {
	for _, tt := range []struct {
		name   string
		result *eval.Result
	}{
		{"block", blockResult()},
		{"pass", passResult()},
		{"hostile", hostileResult()},
	} {
		for _, f := range Formats() {
			t.Run(tt.name+"/"+string(f), func(t *testing.T) {
				var b bytes.Buffer
				if err := Write(&b, tt.result, f); err != nil {
					t.Fatal(err)
				}
				file := filepath.Join("testdata", tt.name+"."+extension(f)+".golden")
				if *update {
					if err := os.WriteFile(file, b.Bytes(), 0o600); err != nil {
						t.Fatal(err)
					}
					return
				}
				want, err := os.ReadFile(file)
				if err != nil {
					t.Fatalf("%v; run go test ./report -update to write it", err)
				}
				if diff := cmp.Diff(string(want), b.String()); diff != "" {
					t.Errorf("%s differs (-want +got); if the change is intended, run go test ./report -update:\n%s", file, diff)
				}
			})
		}
	}
}

// TestEscaping checks each format's escaping, with each hostile string in
// every field.
func TestEscaping(t *testing.T) {
	for _, s := range hostile {
		checkEscaping(t, s)
	}
}

// TestEscapingProperties checks the same for random strings, of any
// characters and of any bytes.
func TestEscapingProperties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := rapid.OneOf(rapid.String(), rapid.StringOf(rapid.RuneFrom(nil, invisibleRanges...)), rapid.Map(rapid.SliceOf(rapid.Byte()), func(b []byte) string { return string(b) })).Draw(t, "s")
		checkEscaping(t, s)
	})
}

// FuzzEscaping checks each format's escaping with fuzzed text in every
// field.
func FuzzEscaping(f *testing.F) {
	for _, s := range hostile {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		checkEscaping(t, s)
	})
}

// invisibleRanges are the ranges of characters a report must escape.
var invisibleRanges = []*unicode.RangeTable{unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp}

// tester is what checkEscaping needs of a test: a *testing.T or a
// *rapid.T.
type tester interface {
	Helper()
	Fatalf(format string, args ...any)
}

// checkEscaping renders a result with s in every field, in every format,
// and checks that no field escapes its place.
func checkEscaping(t tester, s string) {
	t.Helper()
	r := resultWith(s)
	render := func(f Format) string {
		var b bytes.Buffer
		if err := Write(&b, r, f); err != nil {
			t.Fatalf("Write(%s) error = %v", f, err)
		}
		return b.String()
	}

	// Text: each check takes one line, and violations one line each.
	text := render(Text)
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if want := 2 + len(r.Checks) + 2 + 1; len(lines) != want {
		t.Fatalf("text has %d lines, want %d, for %q:\n%s", len(lines), want, s, text)
	}
	for _, line := range lines {
		if i := strings.IndexFunc(line, invisible); i >= 0 {
			t.Fatalf("text shows %U as itself, for %q:\n%s", []rune(line[i:])[0], s, line)
		}
	}

	// Markdown: each table row keeps its five cell borders; HTML, mentions
	// and references are escaped; nothing is invisible.
	md := render(Markdown)
	rows := 0
	for _, line := range strings.Split(md, "\n") {
		if i := strings.IndexFunc(line, invisible); i >= 0 {
			t.Fatalf("Markdown shows %U as itself, for %q:\n%s", []rune(line[i:])[0], s, line)
		}
		for _, ch := range []byte{'<', '>', '`', '[', ']', '*', '_', '&'} {
			if n := unescaped(line, ch); n > 0 && (ch != '&' || strings.Count(line, zeroWidthSpace) < n) {
				t.Fatalf("Markdown has %d unescaped %q, for %q:\n%s", n, ch, s, line)
			}
		}
		if strings.Count(line, "@") != strings.Count(line, "@"+zeroWidthSpace) {
			t.Fatalf("Markdown has a mention that isn't broken up, for %q:\n%s", s, line)
		}
		if strings.HasPrefix(line, "| ") {
			rows++
			if n := unescaped(line, '|'); n != 5 {
				t.Fatalf("Markdown row has %d cell borders, want 5, for %q:\n%s", n, s, line)
			}
		}
	}
	if want := 2 + len(r.Checks); rows != want {
		t.Fatalf("Markdown has %d table rows, want %d, for %q:\n%s", rows, want, s, md)
	}

	// JSON: valid, safe to show and to embed, and it keeps s exactly.
	data := render(JSON)
	if i := strings.IndexFunc(data, func(r rune) bool { return invisible(r) && r != '\n' || r == '<' || r == '>' || r == '&' }); i >= 0 {
		t.Fatalf("JSON shows %U as itself, for %q", []rune(data[i:])[0], s)
	}
	var got jsonReport
	if err := json.Unmarshal([]byte(data), &got); err != nil {
		t.Fatalf("JSON doesn't decode: %v\n%s", err, data)
	}
	want := replaceInvalid(s)
	if got.Contract.Name != want || got.Candidate.Key != want || got.Checks[0].Violations[0].Pointer != want || got.Checks[1].Evidence != want || got.Checks[2].Budget.Breached[0] != want {
		t.Fatalf("JSON changed %q into %+v", s, got)
	}
}

// replaceInvalid replaces each byte of s that isn't UTF-8 with U+FFFD, as
// encoding/json does.
func replaceInvalid(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		b.WriteRune(r)
		i += size
	}
	return b.String()
}

// unescaped counts the times ch appears in line without an odd number of
// backslashes before it.
func unescaped(line string, ch byte) int {
	count, slashes := 0, 0
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\':
			slashes++
			continue
		case line[i] == ch && slashes%2 == 0:
			count++
		}
		slashes = 0
	}
	return count
}

func TestParseFormat(t *testing.T) {
	for _, f := range Formats() {
		if got, err := ParseFormat(string(f)); got != f || err != nil {
			t.Errorf("ParseFormat(%q) = %q, %v", f, got, err)
		}
	}
	if _, err := ParseFormat("html" + rlo); !errors.Is(err, ErrFormat) || strings.Contains(err.Error(), rlo) {
		t.Errorf("ParseFormat(html + RLO) error = %v, want ErrFormat with the RLO escaped", err)
	}
	if err := Write(&bytes.Buffer{}, passResult(), "xml"); !errors.Is(err, ErrFormat) {
		t.Errorf("Write(xml) error = %v, want ErrFormat", err)
	}
}

func TestCut(t *testing.T) {
	for _, tt := range []struct {
		s    string
		n    int
		want string
	}{
		{"abc", 3, "abc"},
		{"abcd", 3, "ab…"},
		{"中文字符", 3, "中文…"},
		{"", 3, ""},
	} {
		if got := cut(tt.s, tt.n); got != tt.want {
			t.Errorf("cut(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
		}
	}
}
