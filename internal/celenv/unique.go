package celenv

import (
	"fmt"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker"
	"github.com/google/cel-go/common"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
)

const uniqueOverload = "zerocular_unique_list"

// uniqueFunction declares zerocular.unique(list) -> bool, which reports
// whether a list holds no two equal elements. It runs in linear time with
// a hash set, where the filter-based CEL equivalent is quadratic and can
// exhaust the cost limit on a large list.
func uniqueFunction() cel.EnvOption {
	return cel.Lib(uniqueLib{})
}

type uniqueLib struct{}

// CompileOptions declares the function and its static cost estimate.
func (uniqueLib) CompileOptions() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Function("zerocular.unique",
			cel.Overload(uniqueOverload, []*cel.Type{cel.ListType(cel.DynType)}, cel.BoolType,
				cel.UnaryBinding(unique))),
		cel.CostEstimatorOptions(checker.OverloadCostEstimate(uniqueOverload, estimateUnique)),
	}
}

// ProgramOptions has nothing to add; the cost counter charges each call
// (see countCost).
func (uniqueLib) ProgramOptions() []cel.ProgramOption {
	return nil
}

func unique(arg ref.Val) ref.Val {
	list, ok := arg.(traits.Lister)
	if !ok {
		return types.MaybeNoSuchOverloadErr(arg)
	}
	seen := make(map[string]struct{})
	var b strings.Builder
	for it := list.Iterator(); it.HasNext() == types.True; {
		b.Reset()
		hashable, err := writeKey(&b, it.Next())
		if err != nil {
			return err
		}
		if !hashable {
			continue
		}
		key := b.String()
		if _, dup := seen[key]; dup {
			return types.False
		}
		seen[key] = struct{}{}
	}
	return types.True
}

// writeKey writes a key for v such that two values have the same key
// exactly when CEL's == says they're equal: 1, 1u and 1.0 share a key, and
// so do two maps with the same entries in another order. It reports a value
// that equals nothing, such as one that holds NaN, as not hashable. It
// returns a CEL error value if v is one.
func writeKey(b *strings.Builder, v ref.Val) (hashable bool, err ref.Val) {
	switch v := v.(type) {
	case types.Null:
		b.WriteString("n")
	case types.Bool:
		if v {
			b.WriteString("t")
		} else {
			b.WriteString("f")
		}
	case types.String:
		writeString(b, "s", string(v))
	case types.Bytes:
		writeString(b, "y", string(v))
	case types.Int:
		b.WriteString("i")
		b.WriteString(strconv.FormatInt(int64(v), 10))
	case types.Uint:
		b.WriteString("i")
		b.WriteString(strconv.FormatUint(uint64(v), 10))
	case types.Double:
		f := float64(v)
		switch {
		case math.IsNaN(f):
			return false, nil
		case math.IsInf(f, 0):
			b.WriteString(strconv.FormatFloat(f, 'g', -1, 64))
		case f == math.Trunc(f):
			// An integral double equals the integer it holds.
			n, _ := new(big.Float).SetFloat64(f).Int(nil)
			b.WriteString("i")
			b.WriteString(n.String())
		default:
			b.WriteString("d")
			b.WriteString(strconv.FormatFloat(f, 'g', -1, 64))
		}
	case types.Timestamp:
		writeString(b, "T", v.UTC().Format(time.RFC3339Nano))
	case types.Duration:
		b.WriteString("D")
		b.WriteString(strconv.FormatInt(int64(v.Duration), 10))
	case *types.Err:
		return false, v
	case traits.Lister:
		b.WriteString("l")
		b.WriteString(strconv.FormatInt(int64(v.Size().(types.Int)), 10))
		b.WriteString("[")
		for it := v.Iterator(); it.HasNext() == types.True; {
			if ok, err := writeKey(b, it.Next()); !ok {
				return false, err
			}
		}
		b.WriteString("]")
	case traits.Mapper:
		// Write the entries in a fixed order, since map order varies.
		var entries []string
		var eb strings.Builder
		for it := v.Iterator(); it.HasNext() == types.True; {
			k := it.Next()
			eb.Reset()
			if ok, err := writeKey(&eb, k); !ok {
				return false, err
			}
			if ok, err := writeKey(&eb, v.Get(k)); !ok {
				return false, err
			}
			entries = append(entries, eb.String())
		}
		slices.Sort(entries)
		b.WriteString("m")
		b.WriteString(strconv.Itoa(len(entries)))
		b.WriteString("{")
		for _, e := range entries {
			writeString(b, "", e)
		}
		b.WriteString("}")
	default:
		writeString(b, "o"+v.Type().TypeName(), fmt.Sprint(v.Value()))
	}
	return true, nil
}

// EqualityKey returns a string that two values share exactly when CEL's ==
// says they're equal, as zerocular.unique compares them. It returns false
// for a value that equals nothing, such as one that holds NaN, and an error
// if v is a CEL error.
func EqualityKey(v ref.Val) (string, bool, error) {
	var b strings.Builder
	ok, errVal := writeKey(&b, v)
	if errVal != nil {
		return "", false, fmt.Errorf("%v", errVal)
	}
	return b.String(), ok, nil
}

// writeString writes s after a tag and its length, so keys stay
// unambiguous when they're nested.
func writeString(b *strings.Builder, tag, s string) {
	b.WriteString(tag)
	b.WriteString(strconv.Itoa(len(s)))
	b.WriteString(":")
	b.WriteString(s)
}

// valueCost is the size of a value, nested elements included, plus CEL's
// usual cost for traversing strings.
func valueCost(v ref.Val) uint64 {
	switch v := v.(type) {
	case types.String:
		return 1 + uint64(math.Ceil(float64(len(v))*common.StringTraversalCostFactor))
	case types.Bytes:
		return 1 + uint64(math.Ceil(float64(len(v))*common.StringTraversalCostFactor))
	case traits.Lister:
		cost := uint64(1)
		for it := v.Iterator(); it.HasNext() == types.True; {
			cost = saturatingAdd(cost, valueCost(it.Next()))
		}
		return cost
	case traits.Mapper:
		cost := uint64(1)
		for it := v.Iterator(); it.HasNext() == types.True; {
			k := it.Next()
			cost = saturatingAdd(cost, saturatingAdd(valueCost(k), valueCost(v.Get(k))))
		}
		return cost
	default:
		return 1
	}
}

// estimateUnique estimates a call's cost as linear in the list's size.
func estimateUnique(estimator checker.CostEstimator, _ *checker.AstNode, args []checker.AstNode) *checker.CallEstimate {
	if len(args) != 1 {
		return nil
	}
	size := args[0].ComputedSize()
	if size == nil {
		size = estimator.EstimateSize(args[0])
	}
	if size == nil {
		return nil
	}
	return &checker.CallEstimate{CostEstimate: checker.CostEstimate{Min: 1, Max: saturatingAdd(1, size.Max)}}
}

func saturatingAdd(a, b uint64) uint64 {
	if a > math.MaxUint64-b {
		return math.MaxUint64
	}
	return a + b
}
