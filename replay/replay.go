package replay

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/itillema/0cular/contract"
	"github.com/itillema/0cular/eval"
)

// builtIn holds every scenario's files except its consumer stub, so that
// the 0cular binary can run the scenarios anywhere.
//
//go:embed */README.md */contract.yaml */schema.json */expected.yaml */baseline.* */candidate.*
var builtIn embed.FS

// Scenario is one replay scenario.
type Scenario struct {
	// Name is the scenario's directory, such as doubled-feature-file.
	Name string
	// Expected is the scenario's expected.yaml.
	Expected Expected
	// files are the scenario's files, rooted at its directory.
	files fs.FS
}

// Expected is a scenario's expected.yaml: the outcome expected at each
// stage of 0cular. expected.schema.json describes it in full.
type Expected struct {
	SchemaVersion int `yaml:"schemaVersion"`
	// Scenario is the scenario's name.
	Scenario string `yaml:"scenario"`
	// Postmortem is the public postmortem the scenario follows.
	Postmortem Postmortem `yaml:"postmortem"`
	// Artifact names the scenario's two versions of its artifact.
	Artifact Versions `yaml:"artifact"`
	// Consumer is how the consumer stub must behave on each version.
	Consumer Consumer `yaml:"consumer"`
	// Stages are the expected outcomes.
	Stages Stages `yaml:"stages"`
}

// Postmortem is a public postmortem.
type Postmortem struct {
	Title string `yaml:"title"`
	URL   string `yaml:"url"`
}

// Versions names a scenario's two versions of its artifact.
type Versions struct {
	Format    contract.Format `yaml:"format"`
	Baseline  string          `yaml:"baseline"`
	Candidate string          `yaml:"candidate"`
}

// Consumer is how a consumer stub must behave on each version.
type Consumer struct {
	Baseline  StubRun `yaml:"baseline"`
	Candidate StubRun `yaml:"candidate"`
}

// StubRun is how a consumer stub must behave on one version.
type StubRun struct {
	ExitCode       int    `yaml:"exitCode"`
	StderrContains string `yaml:"stderrContains"`
}

// Stages are the expected outcomes at each stage of 0cular. Check runs the
// check stage; the other stages arrive with the versions of 0cular that
// implement them, and are kept as they're written until then.
type Stages struct {
	Check   CheckStage     `yaml:"check"`
	Loader  map[string]any `yaml:"loader"`
	Publish map[string]any `yaml:"publish"`
	Rings   map[string]any `yaml:"rings"`
}

// CheckStage is the gate's expected verdict on the candidate, against the
// baseline.
type CheckStage struct {
	Verdict eval.Verdict `yaml:"verdict"`
	// Checks are the checks that don't pass, in the order Check returns
	// them.
	Checks []CheckOutcome `yaml:"checks"`
	Note   string         `yaml:"note"`
}

// CheckOutcome is a check that doesn't pass.
type CheckOutcome struct {
	Kind   eval.Kind   `yaml:"kind"`
	Name   string      `yaml:"name"`
	Status eval.Status `yaml:"status"`
}

func (c CheckOutcome) String() string {
	return fmt.Sprintf("%s %s %s", c.Kind, c.Name, c.Status)
}

// Scenarios returns the scenarios built into 0cular, by name.
func Scenarios() ([]Scenario, error) {
	return Load(builtIn)
}

// Load reads the scenarios in fsys, one in each directory that holds an
// expected.yaml, by name. It rejects an expected.yaml with fields it doesn't
// know.
func Load(fsys fs.FS) ([]Scenario, error) {
	paths, err := fs.Glob(fsys, "*/expected.yaml")
	if err != nil {
		return nil, fmt.Errorf("replay: %w", err)
	}
	scenarios := make([]Scenario, 0, len(paths))
	for _, p := range paths {
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, fmt.Errorf("replay: %w", err)
		}
		var e Expected
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&e); err != nil {
			return nil, fmt.Errorf("replay: %s: %w", p, err)
		}
		dir := path.Dir(p)
		files, err := fs.Sub(fsys, dir)
		if err != nil {
			return nil, fmt.Errorf("replay: %w", err)
		}
		scenarios = append(scenarios, Scenario{Name: dir, Expected: e, files: files})
	}
	slices.SortFunc(scenarios, func(a, b Scenario) int { return strings.Compare(a.Name, b.Name) })
	return scenarios, nil
}

// Contract loads the scenario's contract, with the schema file it names.
func (s Scenario) Contract() (*contract.Contract, error) {
	data, err := fs.ReadFile(s.files, "contract.yaml")
	if err != nil {
		return nil, fmt.Errorf("replay: %s: %w", s.Name, err)
	}
	c, err := contract.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("replay: %s/contract.yaml: %w", s.Name, err)
	}
	if c.Schema != nil {
		if c.Schema.Data, err = fs.ReadFile(s.files, c.Schema.JSONSchema); err != nil {
			return nil, fmt.Errorf("replay: %s: %w", s.Name, err)
		}
	}
	return c, nil
}

// Check runs the scenario's check stage: its candidate against its
// baseline, under its contract.
func (s Scenario) Check(ctx context.Context) (*eval.Result, error) {
	c, err := s.Contract()
	if err != nil {
		return nil, err
	}
	e, err := eval.New(c)
	if err != nil {
		return nil, fmt.Errorf("replay: %s: %w", s.Name, err)
	}
	baseline, err := s.input(ctx, s.Expected.Artifact.Baseline, c.Artifact)
	if err != nil {
		return nil, err
	}
	candidate, err := s.input(ctx, s.Expected.Artifact.Candidate, c.Artifact)
	if err != nil {
		return nil, err
	}
	return e.Check(ctx, candidate, &baseline)
}

// input reads one of the scenario's versions.
func (s Scenario) input(ctx context.Context, name string, a contract.Artifact) (eval.Input, error) {
	f, err := s.files.Open(name)
	if err != nil {
		return eval.Input{}, fmt.Errorf("replay: %s: %w", s.Name, err)
	}
	defer f.Close()
	return eval.ReadInput(ctx, f, path.Join(s.Name, name), a)
}

// Unexpected lists how a check result differs from the scenario's expected
// check stage: in its verdict, in the checks that don't pass, or in a check
// that doesn't pass but has no evidence. It's empty when the result is as
// expected.
func (s Scenario) Unexpected(r *eval.Result) []string {
	want := s.Expected.Stages.Check
	var problems []string
	if r.Verdict != want.Verdict {
		problems = append(problems, fmt.Sprintf("the verdict is %s, not %s", r.Verdict, want.Verdict))
	}
	var got []CheckOutcome
	for _, c := range r.Checks {
		if c.Status == eval.StatusPass || c.Status == eval.StatusSkipped {
			continue
		}
		got = append(got, CheckOutcome{Kind: c.Kind, Name: c.Name, Status: c.Status})
		if c.Evidence == "" {
			problems = append(problems, fmt.Sprintf("%s has no evidence", c.Name))
		}
	}
	if !slices.Equal(got, want.Checks) {
		problems = append(problems, fmt.Sprintf("the checks that don't pass are %s, not %s", listOutcomes(got), listOutcomes(want.Checks)))
	}
	return problems
}

func listOutcomes(outcomes []CheckOutcome) string {
	if len(outcomes) == 0 {
		return "none"
	}
	parts := make([]string, len(outcomes))
	for i, o := range outcomes {
		parts[i] = o.String()
	}
	return "[" + strings.Join(parts, "; ") + "]"
}
