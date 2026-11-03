package replay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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

	"github.com/itillema/0cular/contract"
	"github.com/itillema/0cular/eval"
	"github.com/itillema/0cular/report"
)

// requiredScenarios are the scenarios that must exist. A scenario is added
// here when it's written, so that none can silently disappear.
var requiredScenarios = []string{
	"blank-policy-fields",
	"doubled-feature-file",
	"emptied-record",
}

// blockedAtCheck are the v0.1 scenarios, which the gate must BLOCK at check
// (task 1.7) whatever their expected.yaml says.
var blockedAtCheck = []string{
	"blank-policy-fields",
	"doubled-feature-file",
	"emptied-record",
}

// stageTasks says which build-plan task starts asserting each stage, once
// 0cular implements it.
var stageTasks = map[string]string{
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

// loadScenarios reads the scenarios from their directories, so that the
// tests check the files themselves rather than the copies built into
// 0cular. TestBuiltIn checks that the copies match.
func loadScenarios(t *testing.T) []Scenario {
	t.Helper()
	scenarios, err := Load(os.DirFS("."))
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) == 0 {
		t.Fatal("found no replay scenarios")
	}
	return scenarios
}

// versions returns the paths of a scenario's baseline and candidate.
func versions(s Scenario) []string {
	return []string{
		filepath.Join(s.Name, s.Expected.Artifact.Baseline),
		filepath.Join(s.Name, s.Expected.Artifact.Candidate),
	}
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

// TestBuiltIn checks that the scenarios built into 0cular are the ones in
// this directory, file for file.
func TestBuiltIn(t *testing.T) {
	builtin, err := Scenarios()
	if err != nil {
		t.Fatal(err)
	}
	var got, want []string
	for _, s := range builtin {
		got = append(got, s.Name)
	}
	for _, s := range loadScenarios(t) {
		want = append(want, s.Name)
	}
	if !slices.Equal(got, want) {
		t.Errorf("built-in scenarios = %q, want %q", got, want)
	}
	root, err := os.OpenRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	err = fs.WalkDir(builtIn, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		embedded, err := fs.ReadFile(builtIn, path)
		if err != nil {
			return err
		}
		onDisk, err := fs.ReadFile(root.FS(), path)
		if err != nil {
			return err
		}
		if !bytes.Equal(embedded, onDisk) {
			t.Errorf("the built-in %s differs from the file", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestExpectedFiles(t *testing.T) {
	schema := compileSchema(t, "expected.schema.json")
	for _, s := range loadScenarios(t) {
		t.Run(s.Name, func(t *testing.T) {
			if err := schema.Validate(instance(t, filepath.Join(s.Name, "expected.yaml"))); err != nil {
				t.Errorf("expected.yaml doesn't match expected.schema.json: %v", err)
			}
			if s.Expected.Scenario != s.Name {
				t.Errorf("scenario = %q, want the directory name %q", s.Expected.Scenario, s.Name)
			}
			for _, f := range versions(s) {
				if got, want := filepath.Ext(f), "."+string(s.Expected.Artifact.Format); got != want {
					t.Errorf("%s has extension %q, want %q for format %s", f, got, want, s.Expected.Artifact.Format)
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
		t.Run(s.Name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(s.Name, "README.md"))
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			if !strings.Contains(text, "("+s.Expected.Postmortem.URL+")") {
				t.Errorf("README.md doesn't link the postmortem %s", s.Expected.Postmortem.URL)
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
		for _, path := range versions(s) {
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
		t.Run(s.Name, func(t *testing.T) {
			schema := compileSchema(t, filepath.Join(s.Name, "schema.json"))
			for _, path := range versions(s) {
				if err := schema.Validate(instance(t, path)); err != nil {
					t.Errorf("%s doesn't match schema.json: %v", path, err)
				}
			}
		})
	}
}

// TestContractsLint checks that every scenario's contract passes contract
// lint without a finding, since the scenarios show how to write one.
func TestContractsLint(t *testing.T) {
	for _, s := range loadScenarios(t) {
		c, err := contract.LoadFile(filepath.Join(s.Name, "contract.yaml"))
		if err != nil {
			t.Errorf("%s: %v", s.Name, err)
			continue
		}
		findings, err := eval.Lint(c)
		if err != nil {
			t.Fatalf("%s: Lint() error = %v", s.Name, err)
		}
		for _, f := range findings {
			t.Errorf("%s/contract.yaml:%s", s.Name, f)
		}
	}
}

// TestConsumerStubs proves that each consumer stub exits 0 on its baseline
// and fails on its candidate in the way expected.yaml records.
func TestConsumerStubs(t *testing.T) {
	for _, s := range loadScenarios(t) {
		t.Run(s.Name, func(t *testing.T) {
			stub, ok := stubs[s.Name]
			if !ok {
				t.Fatalf("no consumer stub was built for %s", s.Name)
			}
			baseline, candidate := versions(s)[0], versions(s)[1]

			code, stderr := runStub(t, stub, baseline)
			if want := s.Expected.Consumer.Baseline.ExitCode; code != want {
				t.Errorf("on %s the stub exited with %d, want %d; stderr:\n%s", baseline, code, want, stderr)
			}

			code, stderr = runStub(t, stub, candidate)
			if want := s.Expected.Consumer.Candidate.ExitCode; code != want {
				t.Errorf("on %s the stub exited with %d, want %d; stderr:\n%s", candidate, code, want, stderr)
			}
			if want := s.Expected.Consumer.Candidate.StderrContains; !strings.Contains(stderr, want) {
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

// TestCheckStage checks the gate against each scenario (task 1.7): the
// candidate gets the expected verdict, the expected checks fail, and the
// report names each failing rule and shows its evidence.
func TestCheckStage(t *testing.T) {
	for _, s := range loadScenarios(t) {
		t.Run(s.Name, func(t *testing.T) {
			r, err := s.Check(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, problem := range s.Unexpected(r) {
				t.Error(problem)
			}
			if slices.Contains(blockedAtCheck, s.Name) && r.Verdict != eval.Block {
				t.Errorf("the verdict is %s; the v0.1 scenarios must BLOCK at check", r.Verdict)
			}
			var text bytes.Buffer
			if err := report.Write(&text, r, report.Text); err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(text.String(), "\n")
			for _, c := range r.Checks {
				shown := slices.ContainsFunc(lines, func(line string) bool {
					return strings.Contains(line, " "+c.Name+" ") && strings.Contains(line, c.Evidence)
				})
				if c.Status.Blocks() && !shown {
					t.Errorf("the report doesn't show %s with its evidence %q on one line:\n%s", c.Name, c.Evidence, text.String())
				}
			}
		})
	}
}

// TestUnexpected checks that Unexpected reports each way a result can
// differ from the expected check stage.
func TestUnexpected(t *testing.T) {
	s := Scenario{Name: "s", Expected: Expected{Stages: Stages{Check: CheckStage{
		Verdict: eval.Block,
		Checks:  []CheckOutcome{{Kind: eval.KindInvariant, Name: "cap", Status: eval.StatusFail}},
	}}}}
	asExpected := &eval.Result{Verdict: eval.Block, Checks: []eval.Check{
		{Kind: eval.KindEnvelope, Name: "candidate", Status: eval.StatusPass},
		{Kind: eval.KindInvariant, Name: "cap", Status: eval.StatusFail, Evidence: "size(self.x) = 3, not <= 2"},
		{Kind: eval.KindBudget, Name: "count", Status: eval.StatusSkipped, Evidence: "there's no baseline to compare with"},
	}}
	if got := s.Unexpected(asExpected); len(got) != 0 {
		t.Errorf("Unexpected(as expected) = %q, want nothing", got)
	}
	differs := &eval.Result{Verdict: eval.Pass, Checks: []eval.Check{
		{Kind: eval.KindInvariant, Name: "cap", Status: eval.StatusFail},
		{Kind: eval.KindInvariant, Name: "other", Status: eval.StatusError, Evidence: "no such key: x"},
	}}
	want := []string{
		"the verdict is PASS, not BLOCK",
		"cap has no evidence",
		"the checks that don't pass are [invariant cap fail; invariant other error], not [invariant cap fail]",
	}
	if got := s.Unexpected(differs); !slices.Equal(got, want) {
		t.Errorf("Unexpected(differs) = %q, want %q", got, want)
	}
}

// TestLaterStages lists the stage outcomes that expected.yaml records for
// the stages 0cular doesn't run yet. Each is asserted from the task that
// implements it; until then it's skipped.
func TestLaterStages(t *testing.T) {
	for _, s := range loadScenarios(t) {
		for _, stage := range []struct {
			name    string
			outcome map[string]any
		}{
			{"loader", s.Expected.Stages.Loader},
			{"publish", s.Expected.Stages.Publish},
			{"rings", s.Expected.Stages.Rings},
		} {
			t.Run(s.Name+"/"+stage.name, func(t *testing.T) {
				if len(stage.outcome) == 0 {
					t.Fatalf("expected.yaml has no %s stage", stage.name)
				}
				t.Skipf("the %s stage is asserted from task %s", stage.name, stageTasks[stage.name])
			})
		}
	}
}
