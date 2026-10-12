package eval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/itillema/0cular/contract"
)

// caps returns an artifact description with the default caps.
func caps(format contract.Format) contract.Artifact {
	return contract.Artifact{Format: format, MaxBytes: 1 << 20, MaxDepth: contract.DefaultMaxDepth, MaxNodes: contract.DefaultMaxNodes}
}

type decodeCase struct {
	name string
	src  string
	// artifact changes the default caps, if set.
	artifact func(*contract.Artifact)
	want     any
	// For a failure: the error it wraps, and its pointer, position and
	// message.
	wantErr     error
	wantPointer string
	wantLine    int
	wantColumn  int
	wantMessage string
	// anyPosition skips the position check, for syntax errors whose offset
	// encoding/json reports differently from one Go release to the next.
	anyPosition bool
}

func runDecodeCases(t *testing.T, format contract.Format, cases []decodeCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			a := caps(format)
			if tt.artifact != nil {
				tt.artifact(&a)
			}
			doc, err := Decode(t.Context(), []byte(tt.src), a)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Decode() error = %v", err)
				}
				if diff := cmp.Diff(tt.want, doc.Value); diff != "" {
					t.Errorf("Decode() value mismatch (-want +got):\n%s", diff)
				}
				return
			}
			var de *DecodeError
			if !errors.As(err, &de) {
				t.Fatalf("Decode() error = %v, want a *DecodeError", err)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Decode() error = %v, want one wrapping %v", err, tt.wantErr)
			}
			if !tt.anyPosition && (de.Pointer != tt.wantPointer || de.Line != tt.wantLine || de.Column != tt.wantColumn) {
				t.Errorf("Decode() error at pointer %q, %d:%d; want %q, %d:%d (%v)", de.Pointer, de.Line, de.Column, tt.wantPointer, tt.wantLine, tt.wantColumn, err)
			}
			if !strings.Contains(de.Message, tt.wantMessage) {
				t.Errorf("Decode() message = %q, want it to contain %q", de.Message, tt.wantMessage)
			}
		})
	}
}

func TestDecodeJSON(t *testing.T) {
	runDecodeCases(t, contract.FormatJSON, []decodeCase{
		{
			name: "object",
			src:  `{"a": 1, "b": [true, null, "x"], "c": {"d": -2.5e3}}`,
			want: map[string]any{"a": 1.0, "b": []any{true, nil, "x"}, "c": map[string]any{"d": -2500.0}},
		},
		{name: "top-level string", src: `"x"`, want: "x"},
		{name: "top-level null", src: ` null `, want: nil},
		{name: "empty containers", src: `[[], {}]`, want: []any{[]any{}, map[string]any{}}},
		{name: "largest safe integer", src: `[9007199254740991, -9007199254740991]`, want: []any{9007199254740991.0, -9007199254740991.0}},
		{name: "large float literal", src: `1e20`, want: 1e20},
		{name: "negative zero", src: `[-0, -0.0]`, want: []any{0.0, 0.0}},
		{name: "underflow", src: `1e-400`, want: 0.0},
		{name: "escapes", src: `"\u00e9\ud83d\ude00\n"`, want: "\xc3\xa9\xf0\x9f\x98\x80\n"},
		{name: "escaped key", src: `{"\u0061": 1}`, want: map[string]any{"a": 1.0}},

		{name: "empty", src: "", wantErr: ErrSyntax, wantMessage: "the artifact is empty"},
		{name: "only white space", src: " \n\t", wantErr: ErrSyntax, wantMessage: "the artifact is empty"},
		{name: "byte order mark", src: "\xef\xbb\xbf{}", wantErr: ErrSyntax, wantLine: 1, wantColumn: 1, wantMessage: "byte order mark"},
		{name: "invalid UTF-8", src: "{\"a\": \"\xff\"}", wantErr: ErrEncoding, wantLine: 1, wantColumn: 8, wantMessage: "isn't valid UTF-8"},
		{name: "duplicate key", src: `{"a": 1, "a": 2}`, wantErr: ErrDuplicateKey, wantLine: 1, wantColumn: 10, wantMessage: `duplicate key "a" in the top level`},
		{
			name: "duplicate key behind an escape", src: "{\"x\": {\"a\": 1,\n  \"\\u0061\": 2}}",
			wantErr: ErrDuplicateKey, wantPointer: "/x", wantLine: 2, wantColumn: 3, wantMessage: `duplicate key "a" in /x`,
		},
		{
			name: "integer beyond 2^53", src: `{"id": 9007199254740992}`,
			wantErr: ErrNumber, wantPointer: "/id", wantLine: 1, wantColumn: 8, wantMessage: "the integer 9007199254740992 at /id is beyond",
		},
		{name: "64-bit ID", src: `[12345678901234567890]`, wantErr: ErrNumber, wantPointer: "/0", wantLine: 1, wantColumn: 2, wantMessage: "quote it if it's an identifier"},
		{name: "infinite float", src: `1e400`, wantErr: ErrNumber, wantLine: 1, wantColumn: 1, wantMessage: "too large for a double"},
		{name: "second value", src: "{\"a\": 1}\n {}", wantErr: ErrSyntax, wantLine: 2, wantColumn: 2, wantMessage: "more data after the JSON value"},
		{name: "trailing comma", src: `{"a": 1,}`, wantErr: ErrSyntax, wantLine: 1, wantColumn: 8, wantMessage: "invalid JSON"},
		{name: "truncated", src: `{"a": [1`, wantErr: ErrSyntax, wantPointer: "/a", wantLine: 1, wantColumn: 9, wantMessage: "ends before"},
		{name: "NaN", src: `[NaN]`, wantErr: ErrSyntax, anyPosition: true, wantMessage: "invalid JSON"},
		{name: "lone high surrogate", src: `["ok", "\ud800"]`, wantErr: ErrUnsupported, wantLine: 1, wantColumn: 9, wantMessage: `\ud800 is half of a UTF-16 surrogate pair`},
		{name: "lone low surrogate", src: `"\udc00"`, wantErr: ErrUnsupported, wantLine: 1, wantColumn: 2, wantMessage: "surrogate"},
		{name: "high surrogate before another escape", src: `"\ud800\u0041"`, wantErr: ErrUnsupported, wantLine: 1, wantColumn: 2, wantMessage: "surrogate"},
		{name: "escaped backslash before u", src: `"\\ud800"`, want: `\ud800`},

		// The caps, at and just over each limit.
		{name: "size at maxBytes", src: `[1,2]`, artifact: func(a *contract.Artifact) { a.MaxBytes = 5 }, want: []any{1.0, 2.0}},
		{name: "size over maxBytes", src: `[1, 2]`, artifact: func(a *contract.Artifact) { a.MaxBytes = 5 }, wantErr: ErrTooLarge, wantMessage: "6 bytes, more than maxBytes (5)"},
		{name: "depth at maxDepth", src: `[[1]]`, artifact: func(a *contract.Artifact) { a.MaxDepth = 2 }, want: []any{[]any{1.0}}},
		{
			name: "depth over maxDepth", src: `{"a": [[1]]}`, artifact: func(a *contract.Artifact) { a.MaxDepth = 2 },
			wantErr: ErrTooDeep, wantPointer: "/a/0", wantLine: 1, wantColumn: 8, wantMessage: "values at /a/0 nest deeper than maxDepth (2)",
		},
		{name: "nodes at maxNodes", src: `[1, 2]`, artifact: func(a *contract.Artifact) { a.MaxNodes = 3 }, want: []any{1.0, 2.0}},
		{
			name: "nodes over maxNodes", src: `[1, 2, 3]`, artifact: func(a *contract.Artifact) { a.MaxNodes = 3 },
			wantErr: ErrTooManyNodes, wantPointer: "/2", wantLine: 1, wantColumn: 8, wantMessage: "more than maxNodes (3) values",
		},
	})
}

func TestDecodeYAML(t *testing.T) {
	runDecodeCases(t, contract.FormatYAML, []decodeCase{
		{
			name: "blank field",
			src:  "policies:\n  - id: a\n    limit:\n",
			want: map[string]any{"policies": []any{map[string]any{"id": "a", "limit": nil}}},
		},
		{
			name: "YAML 1.2 scalars",
			src:  "[yes, 0777, 0o17, 0x1F, +12, 2026-10-11, '1:30', .5, ~, True, 1e3, -0]",
			want: []any{"yes", 777.0, 15.0, 31.0, 12.0, "2026-10-11", "1:30", 0.5, nil, true, 1000.0, 0.0},
		},
		{name: "quoted keys", src: "'1': a\n\"true\": b\n", want: map[string]any{"1": "a", "true": "b"}},
		{name: "byte order mark", src: "\xef\xbb\xbfa: 1\n", want: map[string]any{"a": 1.0}},
		{name: "JSON text", src: `{"a":[1,{"b":null}]}`, want: map[string]any{"a": []any{1.0, map[string]any{"b": nil}}}},
		{
			name: "anchor and alias",
			src:  "base: &b {x: 1}\nuse: *b\n",
			want: map[string]any{"base": map[string]any{"x": 1.0}, "use": map[string]any{"x": 1.0}},
		},
		{name: "aliased key", src: "&k name: *k\n", want: map[string]any{"name": "name"}},
		{name: "literal block", src: "s: |\n  line 1\n  line 2\n", want: map[string]any{"s": "line 1\nline 2\n"}},
		{name: "explicit empty document", src: "---\n", want: nil},

		{name: "empty", src: "", wantErr: ErrSyntax, wantMessage: "the artifact is empty"},
		{name: "comment only", src: "# nothing\n", wantErr: ErrSyntax, wantMessage: "the artifact is empty"},
		{name: "two documents", src: "a: 1\n---\nb: 2\n", wantErr: ErrSyntax, wantMessage: "more than one YAML document"},
		{name: "syntax error", src: "a: 1\n b: 2\n", wantErr: ErrSyntax, wantLine: 2, wantMessage: "invalid YAML"},
		{name: "invalid UTF-8", src: "a: \"\xff\"\n", wantErr: ErrEncoding, wantLine: 1, wantColumn: 5, wantMessage: "isn't valid UTF-8"},
		{name: "line separator", src: "a: \"x\xe2\x80\xa8y\"\n", wantErr: ErrEncoding, wantLine: 1, wantColumn: 6, wantMessage: "holds U+2028"},
		{name: "next line", src: "a\xc2\x85b: 1\n", wantErr: ErrEncoding, wantLine: 1, wantColumn: 2, wantMessage: "holds U+0085"},
		{
			name: "duplicate key", src: "a: 1\nb: 2\n'a': 3\n",
			wantErr: ErrDuplicateKey, wantLine: 3, wantColumn: 1, wantMessage: `duplicate key "a" in the top level; it's first on line 1`,
		},
		{name: "integer key", src: "x:\n  1: a\n", wantErr: ErrKey, wantPointer: "/x", wantLine: 2, wantColumn: 3, wantMessage: "the key 1 in /x is an integer; keys must be strings, so quote it"},
		{name: "null key", src: "~: a\n", wantErr: ErrKey, wantLine: 1, wantColumn: 1, wantMessage: "is a null"},
		{name: "sequence key", src: "? [a]\n: b\n", wantErr: ErrKey, wantLine: 1, wantColumn: 3, wantMessage: "is a sequence"},
		{name: "alias key", src: "a: &x k\n*x : v\n", wantErr: ErrKey, wantLine: 2, wantColumn: 1, wantMessage: "the alias *x"},
		{name: "merge key", src: "b: &b {x: 1}\nc:\n  <<: *b\n", wantErr: ErrUnsupported, wantPointer: "/c", wantLine: 3, wantColumn: 3, wantMessage: "merge key (<<)"},
		{name: "standard tag", src: "a: !!str 12\n", wantErr: ErrUnsupported, wantPointer: "/a", wantLine: 1, wantColumn: 4, wantMessage: "the tag !!str at /a isn't supported"},
		{name: "custom tag", src: "a: !point {x: 1}\n", wantErr: ErrUnsupported, wantPointer: "/a", wantLine: 1, wantColumn: 4, wantMessage: "the tag !point"},
		{name: "non-specific tag", src: "a: ! 12\n", wantErr: ErrUnsupported, wantPointer: "/a", wantLine: 1, wantColumn: 4, wantMessage: "the tag ! at /a"},
		{name: "non-specific tag after an anchor", src: "a: &x ! 12\n", wantErr: ErrUnsupported, wantPointer: "/a", wantLine: 1, wantColumn: 4, wantMessage: "the tag !"},
		{name: "non-specific tag before an anchor", src: "- ! &x 12\n", wantErr: ErrUnsupported, wantPointer: "/0", wantLine: 1, wantColumn: 3, wantMessage: "the tag !"},
		{name: "non-specific tag on a key", src: "{! a: 1}", wantErr: ErrUnsupported, wantLine: 1, wantColumn: 2, wantMessage: "the tag !"},
		{name: "integer beyond 2^53", src: "id: 0x20000000000000\n", wantErr: ErrNumber, wantPointer: "/id", wantLine: 1, wantColumn: 5, wantMessage: "the integer 0x20000000000000 at /id is beyond"},
		{name: "64-bit ID", src: "- 12345678901234567890\n", wantErr: ErrNumber, wantPointer: "/0", wantLine: 1, wantColumn: 3, wantMessage: "quote it if it's an identifier"},
		{name: "infinity", src: "x: -.Inf\n", wantErr: ErrNumber, wantPointer: "/x", wantLine: 1, wantColumn: 4, wantMessage: "-.Inf at /x isn't finite"},
		{name: "not a number", src: "x: .nan\n", wantErr: ErrNumber, wantPointer: "/x", wantLine: 1, wantColumn: 4, wantMessage: "isn't finite"},
		{name: "float overflow", src: "x: 1e400\n", wantErr: ErrNumber, wantPointer: "/x", wantLine: 1, wantColumn: 4, wantMessage: "isn't finite"},
		{name: "alias inside its anchor", src: "a: &a [*a]\n", wantErr: ErrUnsupported, wantPointer: "/a/0", wantLine: 1, wantColumn: 8, wantMessage: "inside the value it refers to"},

		// The caps, at and just over each limit.
		{
			name: "aliased depth over maxDepth", src: "a: &a [[1]]\nb: [*a]\n", artifact: func(a *contract.Artifact) { a.MaxDepth = 3 },
			wantErr: ErrTooDeep, wantPointer: "/b/0", wantLine: 2, wantColumn: 5, wantMessage: "the alias *a at /b/0 nests values deeper than maxDepth (3)",
		},
		{name: "depth at maxDepth", src: "a: &a [[1]]\nb: *a\n", artifact: func(a *contract.Artifact) { a.MaxDepth = 3 }, want: map[string]any{"a": []any{[]any{1.0}}, "b": []any{[]any{1.0}}}},
		{
			name: "aliased nodes over maxNodes", src: "a: &a [1, 2]\nb: *a\n", artifact: func(a *contract.Artifact) { a.MaxNodes = 6 },
			wantErr: ErrTooManyNodes, wantPointer: "/b", wantLine: 2, wantColumn: 4, wantMessage: "more than maxNodes (6) values, with YAML aliases expanded",
		},
		{name: "nodes at maxNodes", src: "a: &a [1, 2]\nb: *a\n", artifact: func(a *contract.Artifact) { a.MaxNodes = 7 }, want: map[string]any{"a": []any{1.0, 2.0}, "b": []any{1.0, 2.0}}},
		{name: "aliases at the limit", src: aliases(MaxAliases), want: aliasesValue(MaxAliases)},
		{name: "aliases over the limit", src: aliases(MaxAliases + 1), wantErr: ErrTooManyAliases, wantPointer: "/101", wantLine: 102, wantColumn: 3, wantMessage: "more than 100 YAML aliases"},
	})
}

// aliases returns a YAML sequence that uses n aliases.
func aliases(n int) string {
	var b strings.Builder
	b.WriteString("- &a x\n")
	for range n {
		b.WriteString("- *a\n")
	}
	return b.String()
}

func aliasesValue(n int) any {
	v := make([]any, n+1)
	for i := range v {
		v[i] = "x"
	}
	return v
}

// TestDecodeBillionLaughs checks that an alias bomb fails quickly at the
// node cap, without expanding anything.
func TestDecodeBillionLaughs(t *testing.T) {
	var b strings.Builder
	b.WriteString("l0: &l0 [x, x, x, x, x, x, x, x, x, x]\n")
	for i := 1; i <= 9; i++ {
		fmt.Fprintf(&b, "l%d: &l%d [", i, i)
		for j := range 10 {
			if j > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "*l%d", i-1)
		}
		b.WriteString("]\n")
	}
	start := time.Now()
	_, err := Decode(t.Context(), []byte(b.String()), caps(contract.FormatYAML))
	if !errors.Is(err, ErrTooManyNodes) {
		t.Fatalf("Decode() error = %v, want ErrTooManyNodes", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Decode() took %v to refuse an alias bomb", elapsed)
	}
}

func TestDecodeDocument(t *testing.T) {
	src := []byte(`{"a": 1}`)
	doc, err := Decode(t.Context(), src, caps(contract.FormatJSON))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(src)
	if want := "sha256:" + hex.EncodeToString(sum[:]); doc.Size != 8 || doc.Digest != want {
		t.Errorf("Decode() = Size %d, Digest %q; want 8, %q", doc.Size, doc.Digest, want)
	}
}

func TestDecodeRejectsInvalidCaps(t *testing.T) {
	for _, a := range []contract.Artifact{
		{Format: contract.FormatJSON},
		{Format: contract.FormatJSON, MaxBytes: 1, MaxDepth: 1},
		{Format: "toml", MaxBytes: 1, MaxDepth: 1, MaxNodes: 1},
	} {
		_, err := Decode(t.Context(), []byte("1"), a)
		var de *DecodeError
		if err == nil || errors.As(err, &de) {
			t.Errorf("Decode() with %+v = %v, want an error that isn't a *DecodeError", a, err)
		}
	}
}

func TestDecodeStopsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	src := "[" + strings.Repeat("1,", 10_000) + "1]"
	for _, format := range []contract.Format{contract.FormatJSON, contract.FormatYAML} {
		if _, err := Decode(ctx, []byte(src), caps(format)); !errors.Is(err, context.Canceled) {
			t.Errorf("Decode(%s) with a canceled context = %v, want context.Canceled", format, err)
		}
	}
}

// TestDecodeReplayFixtures decodes the replay suite's baselines and
// candidates under their contracts. The decode model accepts them all; the
// contracts' invariants are what block the candidates.
func TestDecodeReplayFixtures(t *testing.T) {
	contracts, err := filepath.Glob("../replay/*/contract.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range contracts {
		c, err := contract.LoadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, version := range []string{"baseline", "candidate"} {
			path := filepath.Join(filepath.Dir(file), version+"."+string(c.Artifact.Format))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode(t.Context(), data, c.Artifact); err != nil {
				t.Errorf("Decode(%s): %v", path, err)
			}
		}
	}
}
