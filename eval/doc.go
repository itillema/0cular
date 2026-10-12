// Package eval checks candidate artifacts against a contract.
//
// New compiles a contract once. Evaluator.Check then checks any number of
// candidates, in steps:
//
//  1. Envelope: Decode applies the decode model that every format shares
//     (ADR-0005, ADR-0013): the size cap, UTF-8, strict decoding, and the
//     caps on nesting, values and YAML aliases. It returns the artifact as
//     plain Go values, which JSON Schema validation and CEL can both read.
//  2. Schema: the contract's JSON Schema, which CompileSchema compiles.
//  3. Invariants: the contract's CEL rules, each under a cost limit and a
//     timeout (ADR-0003, ADR-0014).
//
// The Result lists every check, in that order, with its status and the
// evidence it found, and a verdict: BLOCK if any check fails or can't run,
// and PASS otherwise. A candidate that fails is a result, not an error.
//
// The package stays small, because loaders embed it: it imports no store
// SDKs, keeps no global state and starts no goroutines, and the contract's
// caps bound the time and memory each step takes.
package eval
