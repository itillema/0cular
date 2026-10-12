package yamlx

import (
	"errors"
	"math"
	"strconv"
	"testing"

	"go.yaml.in/yaml/v3"
	"pgregory.net/rapid"
)

func TestParseDocument(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantErr  error
		wantLine int // for syntax errors
	}{
		{name: "one document", input: "a: 1\n"},
		{name: "explicit document start", input: "---\na: 1\n"},
		{name: "empty input", input: "", wantErr: ErrEmpty},
		{name: "only a comment", input: "# nothing here\n", wantErr: ErrEmpty},
		{name: "two documents", input: "a: 1\n---\nb: 2\n", wantErr: ErrMultipleDocuments},
		{name: "trailing document marker", input: "a: 1\n---\n", wantErr: ErrMultipleDocuments},
		{name: "bad indentation", input: "a: 1\n b: 2\n", wantLine: 2},
		{name: "unclosed flow sequence", input: "a: [1, 2\n", wantLine: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, err := ParseDocument([]byte(tt.input))
			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ParseDocument() error = %v, want %v", err, tt.wantErr)
				}
			case tt.wantLine != 0:
				var syn *SyntaxError
				if !errors.As(err, &syn) {
					t.Fatalf("ParseDocument() error = %v, want a *SyntaxError", err)
				}
				if syn.Line != tt.wantLine {
					t.Errorf("SyntaxError.Line = %d, want %d (%v)", syn.Line, tt.wantLine, syn)
				}
			default:
				if err != nil {
					t.Fatalf("ParseDocument() error = %v", err)
				}
				if root.Kind != yaml.MappingNode {
					t.Errorf("root kind = %v, want a mapping", root.Kind)
				}
			}
		})
	}
}

func TestResolvePlain(t *testing.T) {
	tests := []struct {
		text string
		want Kind
	}{
		{"", KindNull},
		{"~", KindNull},
		{"null", KindNull},
		{"Null", KindNull},
		{"NULL", KindNull},
		{"true", KindBool},
		{"False", KindBool},
		{"TRUE", KindBool},
		// YAML 1.1 booleans are strings under YAML 1.2.
		{"yes", KindString},
		{"no", KindString},
		{"on", KindString},
		{"off", KindString},
		{"y", KindString},
		{"0", KindInt},
		{"42", KindInt},
		{"-17", KindInt},
		{"+3", KindInt},
		{"0777", KindInt},
		{"0o17", KindInt},
		{"0x1F", KindInt},
		{"0xff", KindInt},
		{"1.5", KindFloat},
		{"-.5", KindFloat},
		{"1.", KindFloat},
		{"1e3", KindFloat},
		{"2.5E-4", KindFloat},
		{".inf", KindFloat},
		{"-.Inf", KindFloat},
		{"+.INF", KindFloat},
		{".nan", KindFloat},
		{".NaN", KindFloat},
		// Not numbers under YAML 1.2.
		{"1_000", KindString},
		{"0b101", KindString},
		{"0O17", KindString},
		{"1:30", KindString},
		{"2026-10-11", KindString},
		{"2026-10-11T12:00:00Z", KindString},
		{"-.nan", KindString},
		{"inf", KindString},
		{"hello", KindString},
		{"null value", KindString},
	}
	for _, tt := range tests {
		if got := ResolvePlain(tt.text); got != tt.want {
			t.Errorf("ResolvePlain(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestScalarKind(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		want    Kind
		wantErr error
	}{
		{name: "plain integer", src: "v: 12", want: KindInt},
		{name: "plain YAML 1.1 boolean", src: "v: yes", want: KindString},
		{name: "blank", src: "v:", want: KindNull},
		{name: "double-quoted number", src: `v: "12"`, want: KindString},
		{name: "single-quoted null", src: `v: 'null'`, want: KindString},
		{name: "literal block", src: "v: |\n  12\n", want: KindString},
		{name: "folded block", src: "v: >\n  true\n", want: KindString},
		{name: "explicit standard tag", src: "v: !!str 12", wantErr: ErrTag},
		{name: "custom tag", src: "v: !custom x", wantErr: ErrTag},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, err := ParseDocument([]byte(tt.src))
			if err != nil {
				t.Fatal(err)
			}
			got, err := ScalarKind(root.Content[1])
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ScalarKind() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ScalarKind() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ScalarKind() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScalarKindRejectsNonScalars(t *testing.T) {
	root, err := ParseDocument([]byte("v: [1]"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ScalarKind(root.Content[1]); err == nil {
		t.Error("ScalarKind(sequence) error = nil, want an error")
	}
}

func TestParseBool(t *testing.T) {
	for text, want := range map[string]bool{"true": true, "True": true, "TRUE": true, "false": false, "False": false, "FALSE": false} {
		got, err := ParseBool(text)
		if err != nil || got != want {
			t.Errorf("ParseBool(%q) = %v, %v; want %v", text, got, err, want)
		}
	}
	for _, text := range []string{"yes", "on", "1", "tRuE", ""} {
		if _, err := ParseBool(text); err == nil {
			t.Errorf("ParseBool(%q) error = nil, want an error", text)
		}
	}
}

// errAny, as a test's wantErr, accepts any error.
var errAny = errors.New("any error")

func TestParseInt(t *testing.T) {
	tests := []struct {
		text    string
		want    int64
		wantErr error
	}{
		{text: "0", want: 0},
		{text: "-17", want: -17},
		{text: "+3", want: 3},
		// Leading zeros don't make an integer octal under YAML 1.2.
		{text: "0777", want: 777},
		{text: "0o777", want: 511},
		{text: "0x1F", want: 31},
		{text: "9223372036854775807", want: math.MaxInt64},
		{text: "-9223372036854775808", want: math.MinInt64},
		{text: "9223372036854775808", wantErr: ErrRange},
		{text: "0xFFFFFFFFFFFFFFFF", wantErr: ErrRange},
		{text: "1.5", wantErr: errAny},
		{text: "-0o17", wantErr: errAny},
		{text: "twelve", wantErr: errAny},
	}
	for _, tt := range tests {
		got, err := ParseInt(tt.text)
		if tt.wantErr != nil {
			if err == nil || !errors.Is(tt.wantErr, errAny) && !errors.Is(err, tt.wantErr) {
				t.Errorf("ParseInt(%q) = %d, %v; want error %v", tt.text, got, err, tt.wantErr)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("ParseInt(%q) = %d, %v; want %d", tt.text, got, err, tt.want)
		}
	}
}

func TestParseFloat(t *testing.T) {
	tests := []struct {
		text    string
		want    float64
		wantErr error
	}{
		{text: "1.5", want: 1.5},
		{text: "-.5", want: -0.5},
		{text: "1e3", want: 1000},
		{text: "42", want: 42},
		{text: "0o10", want: 8},
		{text: "0x10", want: 16},
		{text: ".inf", want: math.Inf(1)},
		{text: "-.inf", want: math.Inf(-1)},
		// Too small to represent: it becomes zero, as in strconv.
		{text: "1e-400", want: 0},
		{text: "1e400", wantErr: ErrRange},
		{text: "0x10000000000000000", wantErr: ErrRange},
		{text: "1_000.5", wantErr: errAny},
		{text: "abc", wantErr: errAny},
	}
	for _, tt := range tests {
		got, err := ParseFloat(tt.text)
		if tt.wantErr != nil {
			if err == nil || !errors.Is(tt.wantErr, errAny) && !errors.Is(err, tt.wantErr) {
				t.Errorf("ParseFloat(%q) = %v, %v; want error %v", tt.text, got, err, tt.wantErr)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("ParseFloat(%q) = %v, %v; want %v", tt.text, got, err, tt.want)
		}
	}
	if got, err := ParseFloat(".nan"); err != nil || !math.IsNaN(got) {
		t.Errorf("ParseFloat(.nan) = %v, %v; want NaN", got, err)
	}
}

// TestIntegersRoundTrip checks that every int64, written in any of YAML
// 1.2's integer notations, resolves as an integer and parses back exactly.
func TestIntegersRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.Int64().Draw(t, "n")
		texts := []string{strconv.FormatInt(n, 10)}
		if n >= 0 {
			texts = append(texts, "0o"+strconv.FormatInt(n, 8), "0x"+strconv.FormatInt(n, 16), "+"+strconv.FormatInt(n, 10))
		}
		for _, text := range texts {
			if k := ResolvePlain(text); k != KindInt {
				t.Fatalf("ResolvePlain(%q) = %v, want integer", text, k)
			}
			got, err := ParseInt(text)
			if err != nil || got != n {
				t.Fatalf("ParseInt(%q) = %d, %v; want %d", text, got, err, n)
			}
		}
	})
}

// TestFloatsRoundTrip checks that every finite float64, formatted the way
// Go formats it, resolves as a number and parses back exactly.
func TestFloatsRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		f := rapid.Float64().Filter(func(f float64) bool { return !math.IsInf(f, 0) && !math.IsNaN(f) }).Draw(t, "f")
		text := strconv.FormatFloat(f, 'g', -1, 64)
		if k := ResolvePlain(text); k != KindFloat && k != KindInt {
			t.Fatalf("ResolvePlain(%q) = %v, want a number", text, k)
		}
		got, err := ParseFloat(text)
		if err != nil || got != f {
			t.Fatalf("ParseFloat(%q) = %v, %v; want %v", text, got, err, f)
		}
	})
}

func FuzzParseDocument(f *testing.F) {
	for _, seed := range []string{
		"a: 1\n",
		"a: [1, 2, {b: c}]\n",
		"base: &b {x: 1}\nm:\n  <<: *b\n",
		"a: !!str 12\n",
		"a: |\n  text\n",
		"---\na: 1\n---\nb: 2\n",
		"a: 1\n b: 2\n",
		"",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		root, err := ParseDocument(data)
		if err == nil && root == nil {
			t.Fatal("ParseDocument returned neither a node nor an error")
		}
	})
}

// FuzzResolvePlain checks that the kind ResolvePlain reports always agrees
// with the parser for that kind, and that the other parsers refuse the text.
func FuzzResolvePlain(f *testing.F) {
	for _, seed := range []string{"", "~", "true", "yes", "0777", "0o17", "0x1F", "1.5e3", ".inf", ".nan", "1_000", "1e999", "text"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		_, boolErr := ParseBool(text)
		_, intErr := ParseInt(text)
		_, floatErr := ParseFloat(text)
		switch kind := ResolvePlain(text); kind {
		case KindBool:
			if boolErr != nil || intErr == nil || floatErr == nil {
				t.Fatalf("ResolvePlain(%q) = %v, but ParseBool: %v, ParseInt: %v, ParseFloat: %v", text, kind, boolErr, intErr, floatErr)
			}
		case KindInt:
			if intErr != nil && !errors.Is(intErr, ErrRange) {
				t.Fatalf("ResolvePlain(%q) = %v, but ParseInt failed: %v", text, kind, intErr)
			}
			if floatErr != nil && !errors.Is(floatErr, ErrRange) {
				t.Fatalf("ResolvePlain(%q) = %v, but ParseFloat failed: %v", text, kind, floatErr)
			}
		case KindFloat:
			if intErr == nil {
				t.Fatalf("ResolvePlain(%q) = %v, but ParseInt accepted it", text, kind)
			}
			if floatErr != nil && !errors.Is(floatErr, ErrRange) {
				t.Fatalf("ResolvePlain(%q) = %v, but ParseFloat failed: %v", text, kind, floatErr)
			}
		default:
			if boolErr == nil || intErr == nil || floatErr == nil {
				t.Fatalf("ResolvePlain(%q) = %v, but ParseBool: %v, ParseInt: %v, ParseFloat: %v", text, kind, boolErr, intErr, floatErr)
			}
		}
	})
}
