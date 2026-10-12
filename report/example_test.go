package report_test

import (
	"fmt"
	"os"

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

func ExampleParseFormat() {
	f, err := report.ParseFormat("markdown")
	fmt.Println(f, err)
	_, err = report.ParseFormat("html")
	fmt.Println(err)
	// Output:
	// markdown <nil>
	// unknown report format "html": use text, markdown or json
}
