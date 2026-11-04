package report_test

import (
	"context"
	"fmt"
	"os"

	"github.com/itillema/0cular/contract"
	"github.com/itillema/0cular/diff"
	"github.com/itillema/0cular/eval"
	"github.com/itillema/0cular/report"
)

func ExampleWrite() {
	result := &eval.Result{
		Verdict:   eval.Block,
		Contract:  "bot-features",
		Candidate: eval.Artifact{Key: "config/bot-features.json", Size: 1843, Digest: "sha256:bb12cd34ef56aa78"},
		Checks: []eval.Check{
			{Kind: eval.KindEnvelope, Name: "candidate", Status: eval.StatusPass, Evidence: "1.8 KiB ≤ 5 MiB; valid JSON"},
			{
				Kind: eval.KindInvariant, Name: "feature-cap", Status: eval.StatusFail,
				Message:  "the proxy preallocates 200 features",
				Evidence: "size(self.features) = 240, not <= 200",
			},
		},
	}
	if err := report.Write(os.Stdout, result, report.Text); err != nil {
		fmt.Println(err)
	}
	// Output:
	// 0cular check  bot-features  candidate config/bot-features.json sha256:bb12cd34ef56…  no baseline
	// BLOCK
	//   ✗ invariant  feature-cap  size(self.features) = 240, not <= 200 — the proxy preallocates 200 features
	//   ✓ envelope   candidate    1.8 KiB ≤ 5 MiB; valid JSON
}

func ExampleWriteDiff() {
	c, err := contract.Parse([]byte("apiVersion: 0cular/v1alpha1\nkind: Contract\nmetadata: {name: rate-limits}\nartifact: {format: json, maxBytes: 64KiB}\n"))
	if err != nil {
		fmt.Println(err)
		return
	}
	d, err := diff.New(c)
	if err != nil {
		fmt.Println(err)
		return
	}
	r, err := d.Compare(context.Background(),
		eval.Input{Data: []byte(`{"limits": {"login": 10, "search": 100}}`), Key: "limits.json"},
		eval.Input{Data: []byte(`{"limits": {"login": 10, "search": 1000, "upload": 5}}`), Key: "limits.json"})
	if err != nil {
		fmt.Println(err)
		return
	}
	if err := report.WriteDiff(os.Stdout, r, report.Text); err != nil {
		fmt.Println(err)
	}
	// Output:
	// 0cular diff  rate-limits  baseline limits.json sha256:0a190c388bc5…  candidate limits.json sha256:a0ed213677c7…
	//   ✓ envelope   baseline   40 B ≤ 64 KiB; valid JSON
	//   ✓ envelope   candidate  54 B ≤ 64 KiB; valid JSON
	// 2 changes: 1 added and 1 replaced
	//   replace  /limits/search  100 → 1000
	//   add      /limits/upload  5
}

func ExampleParseFormat() {
	f, err := report.ParseFormat("md")
	fmt.Println(f, err)
	_, err = report.ParseFormat("html")
	fmt.Println(err)
	// Output:
	// md <nil>
	// unknown report format "html": use text, md or json
}
