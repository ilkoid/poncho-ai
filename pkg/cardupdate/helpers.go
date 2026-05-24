// Value conversion helpers for WB API characteristic types.
// Extracted from battle-tested code in fix-card-dimensions and fix-card-fields.
//
// WB Content API expects characteristic values in specific types:
// numbers as int/float64, strings as string, multi-value fields as []string.
// The DB stores all values as JSON arrays (e.g., ["text"] or [42]).
// These helpers convert between DB format and WB API format.
package cardupdate

import (
	"encoding/json"
	"strconv"
	"strings"
)

// UnwrapValue extracts a scalar from single-element JSON arrays.
// [3.0] → 3 (int), [2.5] → 2.5 (float64), ["text"] → "text", [true] → true.
// Multi-element arrays and non-array values pass through unchanged.
func UnwrapValue(val any) any {
	arr, ok := val.([]any)
	if !ok || len(arr) != 1 {
		return val
	}
	switch v := arr[0].(type) {
	case float64:
		if v == float64(int(v)) {
			return int(v)
		}
		return v
	case string, bool:
		return v
	default:
		return val
	}
}

// ConvertCharValue converts a string replacement value to match the type
// of the current JSON value in the card characteristic.
//
// WB API is type-sensitive: sending "42" where it expects 42 fails.
// This function inspects the current value's type and converts accordingly:
//   - number → int or float64
//   - string/[]any → []string via StringToCharArray
func ConvertCharValue(generated string, currentJSON string) any {
	var current any
	if err := json.Unmarshal([]byte(currentJSON), &current); err != nil {
		return StringToCharArray(generated)
	}

	switch unwrapped := UnwrapValue(current).(type) {
	case int, float64:
		if n, err := strconv.Atoi(generated); err == nil {
			return n
		}
		if f, err := strconv.ParseFloat(generated, 64); err == nil {
			return f
		}
		return generated
	case bool:
		return generated
	case string:
		return StringToCharArray(generated)
	case []any:
		return StringToCharArray(generated)
	default:
		_ = unwrapped
		return StringToCharArray(generated)
	}
}

// StringToCharArray splits a comma-separated string into []string for WB API.
// Multi-value characteristics (e.g., seasons, styles) require string arrays.
// Empty result falls back to a single-element slice containing the input.
func StringToCharArray(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return []string{s}
	}
	return result
}
