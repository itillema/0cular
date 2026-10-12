package contract

import "fmt"

// APIVersion is the contract format this build of 0cular reads.
const APIVersion = "0cular/v1alpha1"

// Kind is the kind of every contract file.
const Kind = "Contract"

// Decode caps that apply when a contract doesn't set them (ADR-0005).
const (
	// DefaultMaxDepth is the default artifact.maxDepth.
	DefaultMaxDepth = 64
	// DefaultMaxNodes is the default artifact.maxNodes.
	DefaultMaxNodes = 1_000_000
	// MaxDepthCeiling is the largest artifact.maxDepth a contract may set:
	// the deepest nesting the JSON and YAML parsers read.
	MaxDepthCeiling = 10_000
)

// Contract is a parsed and validated contract: the rules a candidate version
// of one artifact must meet before it's published, and what a loader does
// when it has no version to fall back on. Parse and LoadFile return
// contracts.
type Contract struct {
	// Metadata names the artifact.
	Metadata Metadata
	// Artifact sets the artifact's format and decode caps.
	Artifact Artifact
	// Schema names the JSON Schema the artifact must match, or is nil.
	Schema *Schema
	// Invariants are rules on the candidate alone. A false result or an
	// error blocks the candidate.
	Invariants []Invariant
	// DiffBudgets bound change from the baseline to the candidate.
	DiffBudgets []DiffBudget
	// Report sets what reports show of the artifact.
	Report Report
	// OnFailure says what a loader does when a version fails.
	OnFailure OnFailure

	// Path is the file the contract was loaded from, or empty if it was
	// parsed from bytes.
	Path string
	// Digest is "sha256:" followed by the hex SHA-256 of the contract
	// file's bytes.
	Digest string

	// pos records where each value appears in the file, by path.
	pos map[string]Pos
}

// Position returns where the value at path appears in the contract file.
// A path names a field the way problems do, such as "artifact.maxBytes" or
// "invariants[1].expr". Position returns the zero Pos for a path the file
// doesn't hold.
func (c *Contract) Position(path string) Pos {
	return c.pos[path]
}

// Metadata identifies the artifact a contract governs.
type Metadata struct {
	// Name is the artifact's name: 1 to 63 lowercase letters, digits and
	// hyphens, starting and ending with a letter or digit.
	Name string
}

// Format is an artifact's file format.
type Format string

// The artifact formats this build reads.
const (
	FormatJSON Format = "json"
	FormatYAML Format = "yaml"
)

// Artifact describes the artifact's format and the caps that apply when
// decoding it.
type Artifact struct {
	// Format is the artifact's file format.
	Format Format
	// MaxBytes is the largest artifact allowed, checked before decoding.
	MaxBytes int64
	// MaxDepth is how deeply the artifact's values may nest, from 1 to
	// MaxDepthCeiling: {"a": [1]} nests 2 deep.
	MaxDepth int
	// MaxNodes is how many values the artifact may hold, after YAML
	// aliases are expanded.
	MaxNodes int
}

// Schema names the JSON Schema an artifact must match.
type Schema struct {
	// JSONSchema is the path of a Draft 2020-12 JSON Schema file, relative
	// to the contract file and written with forward slashes.
	JSONSchema string
	// Data is the schema file's content. LoadFile reads it; Parse, which
	// has no file to resolve the path against, leaves it nil.
	Data []byte
}

// Invariant is a rule on the candidate alone, written in CEL. Absolute
// limits belong here.
type Invariant struct {
	// Name identifies the rule in reports and metrics.
	Name string
	// Expr is a CEL expression that must evaluate to true.
	Expr string
	// Message explains why the rule exists. It may be empty.
	Message string
}

// DiffBudget bounds how much the candidate may change from the baseline.
// A measure budget compares one value computed from both versions; a
// collection budget compares two keyed lists. A breach needs approval,
// unless the budget isn't overridable, in which case it blocks.
type DiffBudget struct {
	// Name identifies the budget in reports and metrics.
	Name string

	// Measure is a CEL expression evaluated on the candidate and on the
	// baseline. It's set for measure budgets only.
	Measure string
	// MaxChangePct limits |candidate − baseline| / max(|baseline|, 1) × 100.
	MaxChangePct *float64
	// MaxIncrease limits candidate − baseline.
	MaxIncrease *float64
	// MaxDecrease limits baseline − candidate.
	MaxDecrease *float64

	// Collection is a CEL expression for a list, evaluated on the candidate
	// and on the baseline. It's set for collection budgets only.
	Collection string
	// Key is a CEL expression over item that identifies each element of
	// the collection.
	Key string
	// MaxRemoved limits how many keys the candidate drops.
	MaxRemoved *int64
	// MaxAdded limits how many keys the candidate adds.
	MaxAdded *int64
	// MaxChangedPct limits the percentage of the baseline's elements whose
	// value changes.
	MaxChangedPct *float64

	// Overridable says whether an approval can accept a breach. It
	// defaults to true.
	Overridable bool
}

// IsCollection reports whether b is a collection budget rather than a
// measure budget.
func (b DiffBudget) IsCollection() bool {
	return b.Collection != ""
}

// Report sets what reports show of an artifact.
type Report struct {
	// Redact lists field-name patterns whose values reports hide, in
	// addition to the built-in ones, such as password and token
	// (ADR-0015). A field matches when its name contains a pattern,
	// ignoring case and everything but letters and digits.
	Redact []string
}

// Limits on report.redact.
const (
	// MaxRedactPatterns is how many patterns report.redact may hold.
	MaxRedactPatterns = 64
	// MaxRedactPatternRunes is how long each pattern may be.
	MaxRedactPatternRunes = 64
)

// Policy is what a loader does when it has no last known good to keep.
type Policy string

// The policies for a loader without a last known good.
const (
	// PolicyClosed refuses to start. It's the default.
	PolicyClosed Policy = "closed"
	// PolicyOpen starts with a default value the application provides.
	PolicyOpen Policy = "open"
)

// OnFailure says what a loader does when a version fails. With a last
// known good, every failure keeps it; the policy applies only without one.
type OnFailure struct {
	// NoLastKnownGood applies when a loader has no last known good.
	NoLastKnownGood Policy
	// TrustLastKnownGood keeps a cached last known good that fails
	// re-validation against the current contract. It defaults to false.
	TrustLastKnownGood bool
}

// Pos is a position in a contract file.
type Pos struct {
	// Line is the 1-based line, or 0 if the position is unknown.
	Line int
	// Column is the 1-based column, or 0 if it's unknown.
	Column int
}

// IsValid reports whether the position is known.
func (p Pos) IsValid() bool {
	return p.Line > 0
}

// String returns "line:column", "line", or "" for an unknown position.
func (p Pos) String() string {
	switch {
	case p.Line <= 0:
		return ""
	case p.Column <= 0:
		return fmt.Sprint(p.Line)
	default:
		return fmt.Sprintf("%d:%d", p.Line, p.Column)
	}
}
