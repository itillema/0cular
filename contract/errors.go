package contract

import (
	"errors"
	"strings"
)

// ErrInvalid matches every *Error with errors.Is.
var ErrInvalid = errors.New("invalid contract")

// Problem is one thing wrong with a contract.
type Problem struct {
	// Pos is where the problem is, or the zero Pos for the whole file.
	Pos Pos
	// Path names the field, such as "invariants[1].expr", or is empty for
	// the whole file.
	Path string
	// Message describes the problem.
	Message string
}

// String returns the problem as "line:column: path: message", leaving out
// the parts it doesn't have.
func (p Problem) String() string {
	var b strings.Builder
	if p.Pos.IsValid() {
		b.WriteString(p.Pos.String())
		b.WriteString(": ")
	}
	if p.Path != "" {
		b.WriteString(p.Path)
		b.WriteString(": ")
	}
	b.WriteString(p.Message)
	return b.String()
}

// Error is the error Parse and LoadFile return for an invalid contract. It
// lists every problem found, in file order.
type Error struct {
	// File is the contract file's path, or empty if the contract was parsed
	// from bytes.
	File string
	// Problems lists what's wrong. It's never empty.
	Problems []Problem
}

// Error returns one line per problem, each starting with the file name and
// position, as compilers report errors.
func (e *Error) Error() string {
	var b strings.Builder
	for i, p := range e.Problems {
		if i > 0 {
			b.WriteByte('\n')
		}
		if e.File != "" {
			b.WriteString(e.File)
			b.WriteByte(':')
			if p.Pos.IsValid() {
				b.WriteString(p.Pos.String())
				b.WriteByte(':')
			}
			b.WriteByte(' ')
			p.Pos = Pos{}
		}
		b.WriteString(p.String())
	}
	return b.String()
}

// Is reports whether target is ErrInvalid.
func (e *Error) Is(target error) bool {
	return target == ErrInvalid
}
