// Package scrub provides preventive substring scrubbing for WB API downloaders.
//
// A Replacer rewrites sensitive substrings (e.g. brand names) in the string and
// json.RawMessage fields of decoded structs at data-load time — before persistence.
// It is the load-time counterpart to cmd/fix-utilities/fix-scrub-substring (which
// scrubs already-stored data post-mortem). The two are complementary: preventive
// scrub covers new writes; the SQL tool remains the backstop for historical data
// and for columns rebuilt at write time (e.g. skus_json).
//
// Semantics mirror the SQL tool: case-insensitive global literal replace by default
// (parity with regexp_replace(..., 'gi')), replacement inserted verbatim. Coverage
// includes string fields and json.RawMessage fields (so characteristic values,
// stored as JSON, are reached), recursing into nested structs, slices, arrays, and
// pointers. Map keys and interface-typed fields are left intact (see apply.go).
//
// Allocation discipline: the recursive core takes reflect.Value (not any), so the
// walk itself performs zero per-field interface boxing; every replace is gated on a
// match check, so non-matching fields allocate nothing.
package scrub

// Rule is one find→replace substitution.
type Rule struct {
	// Find is the substring to match. It is always treated as a literal
	// (regexp.QuoteMeta), never as a regex pattern.
	Find string `yaml:"find"`

	// Replace is inserted verbatim wherever Find matches. Literal semantics
	// mean characters like '$' and '\' have no special meaning (no $1 expansion).
	Replace string `yaml:"replace"`

	// CaseSensitive defaults to false (case-insensitive) — parity with the SQL
	// tool's regexp_replace(..., 'gi'). Set true for exact-case matching.
	CaseSensitive bool `yaml:"case_sensitive"`
}

// Rules is the YAML root for a scrub-rules file.
//
// Example:
//
//	rules:
//	  - find: "PlayToday"
//	    replace: "[PlayBrand]"
//	    # case_sensitive defaults to false (case-insensitive)
type Rules struct {
	Rules []Rule `yaml:"rules"`
}
