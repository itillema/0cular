package replay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

// requiredScenarios are the scenarios that must exist. A scenario is added
// here when it's written, so that none can silently disappear.
var requiredScenarios = []string{
	"blank-policy-fields",
	"doubled-feature-file",
	"emptied-record",
}

// stageTasks says which build-plan task starts asserting each stage, once
// 0cular implements it.
var stageTasks = map[string]string{
	"check":   "1.7",
	"loader":  "2.5",
	"publish": "3.6",
	"rings":   "4.6",
}

// stubs maps each scenario's name to its consumer stub, which TestMain builds
// once for all tests.
var stubs = map[string]string{}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "0cular-replay-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "replay: creating a build directory:", err)
		os.Exit(1)
	}
	if err := buildStubs(dir); err != nil {
		fmt.Fprintln(os.Stderr, "replay:", err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// buildStubs builds every scenario's consumer stub into dir and records it in
// stubs. -buildvcs=false keeps the builds independent of the repository's
// state.
func buildStubs(dir string) error {
	mains, err := filepath.Glob(filepath.Join("*", "consumer", "main.go"))
	if err != nil {
		return err
	}
	for _, main := range mains {
		pkgDir := filepath.Dir(main)
		name := filepath.Dir(pkgDir)
		out := filepath.Join(dir, name, "consumer")
		if runtime.GOOS == "windows" {
			out += ".exe"
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
			return err
		}
		build := exec.Command("go", "build", "-buildvcs=false", "-o", out, "./"+filepath.ToSlash(pkgDir))
		if output, err := build.CombinedOutput(); err != nil {
			return fmt.Errorf("building the %s consumer stub: %w\n%s", name, err, output)
		}
		stubs[name] = out
	}
	return nil
}

// expectation is a scenario's expected.yaml. expected.schema.json describes
// it in full; the tests read only these fields.
type expectation struct {
	SchemaVersion int    `yaml:"schemaVersion"`
	Scenario      string `yaml:"scenario"`
	Postmortem    struct {
		Title string `yaml:"title"`
		URL   string `yaml:"url"`
	} `yaml:"postmortem"`
	Artifact struct {
		Format    string `yaml:"format"`
		Baseline  string `yaml:"baseline"`
		Candidate string `yaml:"candidate"`
	} `yaml:"artifact"`
	Consumer struct {
		Baseline  stubRun `yaml:"baseline"`
		Candidate stubRun `yaml:"candidate"`
	} `yaml:"consumer"`
	Stages map[string]any `yaml:"stages"`
}

// stubRun is how a consumer stub must behave on one file.
type stubRun struct {
	ExitCode       int    `yaml:"exitCode"`
	StderrContains string `yaml:"stderrContains"`
}

// scenario is a replay scenario: its directory name and its expectations.
type scenario struct {
	name   string
	expect expectation
}

// files returns the scenario's baseline and candidate paths.
func (s scenario) files() []string {
	return []string{
		filepath.Join(s.name, s.expect.Artifact.Baseline),
		filepath.Join(s.name, s.expect.Artifact.Candidate),
	}
}

// loadScenarios reads every scenario's expected.yaml, rejecting unknown fields.
func loadScenarios(t *testing.T) []scenario {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("*", "expected.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("found no replay scenarios")
	}
	scenarios := make([]scenario, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var e expectation
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&e); err != nil {
			t.Fatalf("decoding %s: %v", path, err)
		}
		scenarios = append(scenarios, scenario{name: filepath.Dir(path), expect: e})
	}
	return scenarios
}

// compileSchema compiles the JSON Schema in the file at path.
func compileSchema(t *testing.T, path string) *jsonschema.Schema {
	t.Helper()
	schema, err := jsonschema.NewCompiler().Compile(path)
	if err != nil {
		t.Fatalf("compiling %s: %v", path, err)
	}
	return schema
}

// instance reads a JSON or YAML file into a value that a schema can validate.
func instance(t *testing.T, path string) any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(path) == ".yaml" {
		var v any
		if err := yaml.Unmarshal(data, &v); err != nil {
			t.Fatalf("decoding %s: %v", path, err)
		}
		if data, err = json.Marshal(v); err != nil {
			t.Fatalf("converting %s to JSON: %v", path, err)
		}
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	return v
}

// TestScenarioLayout checks that every scenario directory has the files a
// scenario needs, and that the required scenarios exist.
func TestScenarioLayout(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		names = append(names, e.Name())
		t.Run(e.Name(), func(t *testing.T) {
			for _, f := range []string{"README.md", "contract.yaml", "schema.json", "expected.yaml", filepath.Join("consumer", "main.go")} {
				if _, err := os.Stat(filepath.Join(e.Name(), f)); err != nil {
					t.Errorf("missing %s", filepath.ToSlash(f))
				}
			}
		})
	}
	for _, want := range requiredScenarios {
		if !slices.Contains(names, want) {
			t.Errorf("required scenario %s is missing", want)
		}
	}
}

func TestExpectedFiles(t *testing.T) {
	schema := compileSchema(t, "expected.schema.json")
	for _, s := range loadScenarios(t) {
		t.Run(s.name, func(t *testing.T) {
			if err := schema.Validate(instance(t, filepath.Join(s.name, "expected.yaml"))); err != nil {
				t.Errorf("expected.yaml doesn't match expected.schema.json: %v", err)
			}
			if s.expect.Scenario != s.name {
				t.Errorf("scenario = %q, want the directory name %q", s.expect.Scenario, s.name)
			}
			for _, f := range s.files() {
				if got, want := filepath.Ext(f), "."+s.expect.Artifact.Format; got != want {
					t.Errorf("%s has extension %q, want %q for format %s", f, got, want, s.expect.Artifact.Format)
				}
				if _, err := os.Stat(f); err != nil {
					t.Errorf("artifact file: %v", err)
				}
			}
		})
	}
}

// TestReadmes checks that every scenario README links its postmortem and says
// plainly that the data is synthetic and that the project isn't affiliated
// with the company. It doesn't fetch the link: tests stay offline.
func TestReadmes(t *testing.T) {
	for _, s := range loadScenarios(t) {
		t.Run(s.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(s.name, "README.md"))
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			if !strings.Contains(text, "("+s.expect.Postmortem.URL+")") {
				t.Errorf("README.md doesn't link the postmortem %s", s.expect.Postmortem.URL)
			}
			lower := strings.ToLower(text)
			for _, word := range []string{"synthetic", "affiliated"} {
				if !strings.Contains(lower, word) {
					t.Errorf("README.md doesn't say the data is synthetic and unaffiliated (no %q)", word)
				}
			}
		})
	}
}

// TestFixtureBytes checks that the baseline and candidate files are exactly
// as intended: Git leaves their bytes alone (-text), so nothing else would
// catch a stray CRLF or byte-order mark.
func TestFixtureBytes(t *testing.T) {
	for _, s := range loadScenarios(t) {
		for _, path := range s.files() {
			t.Run(filepath.ToSlash(path), func(t *testing.T) {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.HasPrefix(data, []byte("\xef\xbb\xbf")) {
					t.Error("starts with a UTF-8 byte-order mark")
				}
				if bytes.ContainsRune(data, '\r') {
					t.Error("contains a carriage return; fixtures use LF line endings")
				}
				if !utf8.Valid(data) {
					t.Error("isn't valid UTF-8")
				}
				if !bytes.HasSuffix(data, []byte("\n")) {
					t.Error("doesn't end with a newline")
				}
			})
		}
	}
}

// TestScenarioSchemas checks that each scenario's schema accepts both its
// baseline and its candidate: the candidate is well-formed, and what breaks
// the consumer is something a schema can't see.
func TestScenarioSchemas(t *testing.T) {
	for _, s := range loadScenarios(t) {
		t.Run(s.name, func(t *testing.T) {
			schema := compileSchema(t, filepath.Join(s.name, "schema.json"))
			for _, path := range s.files() {
				if err := schema.Validate(instance(t, path)); err != nil {
					t.Errorf("%s doesn't match schema.json: %v", path, err)
				}
			}
		})
	}
}

// TestConsumerStubs proves that each consumer stub exits 0 on its baseline
// and fails on its candidate in the way expected.yaml records.
func TestConsumerStubs(t *testing.T) {
	for _, s := range loadScenarios(t) {
		t.Run(s.name, func(t *testing.T) {
			stub, ok := stubs[s.name]
			if !ok {
				t.Fatalf("no consumer stub was built for %s", s.name)
			}
			baseline, candidate := s.files()[0], s.files()[1]

			code, stderr := runStub(t, stub, baseline)
			if want := s.expect.Consumer.Baseline.ExitCode; code != want {
				t.Errorf("on %s the stub exited with %d, want %d; stderr:\n%s", baseline, code, want, stderr)
			}

			code, stderr = runStub(t, stub, candidate)
			if want := s.expect.Consumer.Candidate.ExitCode; code != want {
				t.Errorf("on %s the stub exited with %d, want %d; stderr:\n%s", candidate, code, want, stderr)
			}
			if want := s.expect.Consumer.Candidate.StderrContains; !strings.Contains(stderr, want) {
				t.Errorf("on %s the stub's stderr doesn't contain %q:\n%s", candidate, want, stderr)
			}
		})
	}
}

// runStub runs a consumer stub on one file and returns its exit code and
// error output.
func runStub(t *testing.T, stub, file string) (code int, stderr string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, stub, file)
	// A developer's GOTRACEBACK=crash would turn the stubs' panics into aborts.
	cmd.Env = append(os.Environ(), "GOTRACEBACK=single")
	cmd.WaitDelay = 10 * time.Second
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	err := cmd.Run()
	if err == nil {
		return 0, errOut.String()
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("running %s %s: %v", stub, file, err)
	}
	return exitErr.ExitCode(), errOut.String()
}

// TestStages lists the stage outcomes that expected.yaml records. Each stage
// is asserted from the task that implements it; until then it's skipped.
func TestStages(t *testing.T) {
	for _, s := range loadScenarios(t) {
		for _, stage := range []string{"check", "loader", "publish", "rings"} {
			t.Run(s.name+"/"+stage, func(t *testing.T) {
				if _, ok := s.expect.Stages[stage]; !ok {
					t.Fatalf("expected.yaml has no %s stage", stage)
				}
				t.Skipf("the %s stage is asserted from task %s", stage, stageTasks[stage])
			})
		}
	}
}
