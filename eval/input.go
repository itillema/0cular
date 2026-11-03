package eval

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"

	"github.com/itillema/0cular/contract"
)

// ReadInput reads one version of an artifact from r, for Check. key is the
// version's key in its store, or its file path. ReadInput keeps at most
// a.MaxBytes+1 bytes in memory: it reads a larger version to its end, for
// its size and digest, and Check reports it too large without decoding it.
// It returns ctx's error if ctx ends first.
func ReadInput(ctx context.Context, r io.Reader, key string, a contract.Artifact) (Input, error) {
	keep := &prefix{max: a.MaxBytes}
	if keep.max < math.MaxInt64 {
		keep.max++
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(h, keep), contextReader{ctx: ctx, r: r})
	if err != nil {
		return Input{}, fmt.Errorf("read %s: %w", key, err)
	}
	return Input{Data: keep.buf.Bytes(), Key: key, Size: n, Digest: "sha256:" + hex.EncodeToString(h.Sum(nil))}, nil
}

// prefix keeps the first max bytes written to it, and discards the rest.
type prefix struct {
	buf bytes.Buffer
	max int64
}

func (p *prefix) Write(b []byte) (int, error) {
	if room := p.max - int64(p.buf.Len()); room > 0 {
		p.buf.Write(b[:min(int64(len(b)), room)])
	}
	return len(b), nil
}

// contextReader stops reading once its context ends.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c contextReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// partial reports whether in holds only the start of its version: ReadInput
// set its size, which is larger than its data.
func (in Input) partial() bool {
	return in.Size > int64(len(in.Data))
}
