package eval

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"pgregory.net/rapid"

	"github.com/itillema/0cular/contract"
)

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestReadInput(t *testing.T) {
	a := contract.Artifact{Format: contract.FormatJSON, MaxBytes: 10, MaxDepth: 64, MaxNodes: 100}
	small := `{"a": 1}`
	in, err := ReadInput(t.Context(), strings.NewReader(small), "small.json", a)
	if err != nil {
		t.Fatal(err)
	}
	if string(in.Data) != small || in.Key != "small.json" || in.Size != int64(len(small)) || in.Digest != digestOf(small) || in.partial() {
		t.Errorf("ReadInput(small) = %+v", in)
	}

	big := strings.Repeat("x", 100)
	in, err = ReadInput(t.Context(), strings.NewReader(big), "big.json", a)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Data) != 11 || in.Size != 100 || in.Digest != digestOf(big) || !in.partial() {
		t.Errorf("ReadInput(big) kept %d bytes, size %d, digest %s, partial %v; want 11 bytes, size 100 and the whole digest",
			len(in.Data), in.Size, in.Digest, in.partial())
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ReadInput(ctx, strings.NewReader(big), "big.json", a); !errors.Is(err, context.Canceled) {
		t.Errorf("ReadInput with a canceled context error = %v, want context.Canceled", err)
	}
}

// TestReadInputProperties checks ReadInput on any version, cap and read
// size: it keeps the first maxBytes+1 bytes, and reports the whole
// version's size and digest.
func TestReadInputProperties(t *testing.T) {
	ctx := t.Context()
	rapid.Check(t, func(rt *rapid.T) {
		data := rapid.SliceOfN(rapid.Byte(), 0, 300).Draw(rt, "data")
		maxBytes := rapid.Int64Range(1, 200).Draw(rt, "maxBytes")
		var r io.Reader = bytes.NewReader(data)
		if rapid.Bool().Draw(rt, "oneByteReads") {
			r = iotest.OneByteReader(r)
		}
		a := contract.Artifact{Format: contract.FormatJSON, MaxBytes: maxBytes, MaxDepth: 64, MaxNodes: 100}
		in, err := ReadInput(ctx, r, "v.json", a)
		if err != nil {
			rt.Fatal(err)
		}
		kept := data[:min(int64(len(data)), maxBytes+1)]
		if !bytes.Equal(in.Data, kept) || in.Size != int64(len(data)) || in.Digest != digestOf(string(data)) {
			rt.Fatalf("ReadInput() kept %d bytes, size %d, digest %s; want %d bytes, size %d, digest %s",
				len(in.Data), in.Size, in.Digest, len(kept), len(data), digestOf(string(data)))
		}
		if got, want := in.partial(), int64(len(data)) > maxBytes+1; got != want {
			rt.Fatalf("partial() = %v, want %v", got, want)
		}
	})
}

// TestCheckPartialInput checks that a version larger than maxBytes is
// blocked with its real size and digest, though only its start was read.
func TestCheckPartialInput(t *testing.T) {
	c := mustContract(t, "apiVersion: 0cular/v1alpha1\nkind: Contract\nmetadata: {name: big}\nartifact: {format: json, maxBytes: 1KiB}\n"+
		"invariants:\n  - {name: anything, expr: \"true\"}\n", "")
	e := mustEvaluator(t, c)
	big := `{"pad": "` + strings.Repeat("x", 5000) + `"}`
	in, err := ReadInput(t.Context(), strings.NewReader(big), "big.json", c.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.Check(t.Context(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	envelope := r.Checks[0]
	if r.Verdict != Block || envelope.Status != StatusFail || envelope.Evidence != "the artifact is 5011 bytes, more than maxBytes (1024)" {
		t.Errorf("Check() = %s, envelope %s %q; want BLOCK with the real size", r.Verdict, envelope.Status, envelope.Evidence)
	}
	if r.Candidate.Size != int64(len(big)) || r.Candidate.Digest != digestOf(big) {
		t.Errorf("Candidate = %+v, want the whole version's size and digest", r.Candidate)
	}

	// Data cut short of a size that the contract allows is a caller's
	// mistake, so Check can't judge the version.
	r, err = e.Check(t.Context(), Input{Data: []byte(`{"a"`), Key: "cut.json", Size: 100, Digest: digestOf("x")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Checks[0]; r.Verdict != Block || got.Status != StatusError || !strings.Contains(got.Evidence, "only 4 of the version's 100 bytes were read") {
		t.Errorf("Check(cut short) = %s, %s %q; want BLOCK with an error", r.Verdict, got.Status, got.Evidence)
	}
}
