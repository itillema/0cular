package contract

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// sizeUnits maps each unit a size may carry to its number of bytes. Sizes
// use binary units only, so 1MiB can't be mistaken for a million bytes.
var sizeUnits = map[string]int64{"": 1, "B": 1, "KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30}

var (
	sizePattern     = regexp.MustCompile(`^([0-9]+)(B|KiB|MiB|GiB)?$`)
	spacedSize      = regexp.MustCompile(`^([0-9]+)\s+(B|KiB|MiB|GiB)$`)
	decimalUnitSize = regexp.MustCompile(`^([0-9]+)\s*([kKMG])B?$`)
)

// parseSize parses a size: a whole number of bytes, optionally followed by
// B, KiB, MiB or GiB, such as 5MiB. It must be at least 1 byte.
func parseSize(text string) (int64, error) {
	m := sizePattern.FindStringSubmatch(text)
	if m == nil {
		switch {
		case spacedSize.MatchString(text):
			s := spacedSize.FindStringSubmatch(text)
			return 0, fmt.Errorf("write the size without a space, as %s%s", s[1], s[2])
		case decimalUnitSize.MatchString(text):
			s := decimalUnitSize.FindStringSubmatch(text)
			unit := map[string]string{"k": "KiB", "K": "KiB", "M": "MiB", "G": "GiB"}[s[2]]
			return 0, fmt.Errorf("sizes use binary units: write %s%s (1 %s is %d bytes)", s[1], unit, unit, sizeUnits[unit])
		}
		return 0, errors.New("must be a size: a whole number of bytes, or a whole number followed by B, KiB, MiB or GiB, such as 5MiB")
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	unit := sizeUnits[m[2]]
	if err != nil || n > math.MaxInt64/unit {
		return 0, errors.New("is too large")
	}
	if n == 0 {
		return 0, errors.New("must be at least 1 byte")
	}
	return n * unit, nil
}
