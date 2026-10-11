// Package replay holds 0cular's replay suite: synthetic scenarios shaped like
// public postmortems, each with a consumer stub that fails the way the real
// consumer did, and the outcome expected at each stage of 0cular.
//
// Each scenario lives in its own directory:
//
//	README.md       the incident, a link to its postmortem, and what's synthetic
//	contract.yaml   the contract that guards the artifact
//	schema.json     the artifact's JSON Schema
//	baseline.*      a good version of the artifact
//	candidate.*     the bad version
//	consumer/       a stub that exits 0 on the baseline and fails on the candidate
//	expected.yaml   the expected outcome at each stage, under expected.schema.json
//
// The package's tests prove each stub's behavior and check every scenario's
// files. The data is synthetic, and 0cular isn't affiliated with the
// companies whose postmortems the scenarios follow.
package replay
