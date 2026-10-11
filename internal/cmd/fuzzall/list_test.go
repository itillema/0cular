package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

// Event streams in the shape go test -json -list prints them (Go 1.27).
const (
	listOnePackage = `{"Time":"2026-10-10T14:48:44.2467318-05:00","Action":"start","Package":"example.test/a"}
{"Time":"2026-10-10T14:48:45.3329923-05:00","Action":"output","Package":"example.test/a","Output":"FuzzDecode\n"}
{"Time":"2026-10-10T14:48:45.3330001-05:00","Action":"output","Package":"example.test/a","Output":"FuzzEval\n"}
{"Time":"2026-10-10T14:48:45.3344315-05:00","Action":"output","Package":"example.test/a","Output":"ok  \texample.test/a\t1.087s\n"}
{"Time":"2026-10-10T14:48:45.3344315-05:00","Action":"pass","Package":"example.test/a","Elapsed":1.088}
`
	listNoTests = `{"Action":"start","Package":"example.test/b"}
{"Action":"output","Package":"example.test/b","Output":"?   \texample.test/b\t[no test files]\n"}
{"Action":"skip","Package":"example.test/b","Elapsed":0}
`
	listBuildFailure = `{"ImportPath":"example.test/c [example.test/c.test]","Action":"build-output","Output":"# example.test/c [example.test/c.test]\n"}
{"ImportPath":"example.test/c [example.test/c.test]","Action":"build-output","Output":"c\\c_test.go:5:28: undefined: FuzzFake\n"}
{"ImportPath":"example.test/c [example.test/c.test]","Action":"build-fail"}
{"Action":"start","Package":"example.test/c"}
{"Action":"output","Package":"example.test/c","Output":"FAIL\texample.test/c [build failed]\n","OutputType":"frame"}
{"Action":"fail","Package":"example.test/c","Elapsed":0,"FailedBuild":"example.test/c [example.test/c.test]"}
`
)

func TestParseList(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []target
	}{
		{
			name:  "one package with two targets",
			input: listOnePackage,
			want:  []target{{"example.test/a", "FuzzDecode"}, {"example.test/a", "FuzzEval"}},
		},
		{
			name:  "a package without tests between packages with targets",
			input: listOnePackage + listNoTests + strings.ReplaceAll(listOnePackage, "example.test/a", "example.test/d"),
			want: []target{
				{"example.test/a", "FuzzDecode"},
				{"example.test/a", "FuzzEval"},
				{"example.test/d", "FuzzDecode"},
				{"example.test/d", "FuzzEval"},
			},
		},
		{
			name:  "build output is not a target",
			input: listBuildFailure,
			want:  nil,
		},
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
		{
			name: "only output lines that are fuzz target names count",
			input: `{"Action":"output","Package":"example.test/e","Output":"Fuzz\n"}
{"Action":"output","Package":"example.test/e","Output":"Fuzzer\n"}
{"Action":"output","Package":"example.test/e","Output":"TestFuzzThing\n"}
{"Action":"output","Package":"example.test/e","Output":"Fuzz Thing\n"}
{"Action":"output","Package":"","Output":"FuzzNoPackage\n"}
{"Action":"start","Package":"example.test/e","Output":"FuzzNotOutput\n"}
{"Action":"output","Package":"example.test/e","Output":"Fuzz_Ok9\r\n"}
`,
			want: []target{{"example.test/e", "Fuzz"}, {"example.test/e", "Fuzz_Ok9"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseList(strings.NewReader(tt.input))
			if err != nil {
				t.Fatalf("parseList() error = %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("parseList() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseListMalformed(t *testing.T) {
	_, err := parseList(strings.NewReader(listOnePackage + "{\"Action\":"))
	if err == nil {
		t.Fatal("parseList() error = nil, want an error for truncated JSON")
	}
}

func TestIsFuzzName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"Fuzz", true},
		{"FuzzDecode", true},
		{"Fuzz_decode", true},
		{"Fuzz9", true},
		{"FuzzÉtat", true},
		{"Fuzzer", false},
		{"Fuzzé", false},
		{"TestFuzz", false},
		{"fuzzDecode", false},
		{"Fuzz-Decode", false},
		{"Fuzz Decode", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isFuzzName(tt.name); got != tt.want {
			t.Errorf("isFuzzName(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func FuzzParseList(f *testing.F) {
	for _, seed := range []string{listOnePackage, listNoTests, listBuildFailure, "", "{", "[]", "null"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		targets, err := parseList(bytes.NewReader(data))
		if err != nil {
			return
		}
		for _, tg := range targets {
			if tg.Package == "" || !isFuzzName(tg.Name) {
				t.Fatalf("parseList returned an invalid target: %+v", tg)
			}
		}
	})
}
