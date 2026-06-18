package scrub

import (
	"fmt"
	"reflect"
	"regexp"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

// compiledRule is a single compiled find→replace directive.
// re matches Find as a literal (QuoteMeta), optionally case-insensitive.
// repl is the verbatim replacement bytes (used for both string and []byte fields).
type compiledRule struct {
	re   *regexp.Regexp
	repl []byte
}

// Replacer applies a fixed set of compiled rules to strings and struct fields.
//
// A Replacer is safe for concurrent use: compiledRules are immutable after
// construction, and the per-type field-plan cache is guarded by an RWMutex.
type Replacer struct {
	rules []compiledRule

	// planCache memoizes, per reflect.Type, which fields to scrub (string /
	// json.RawMessage) and which to recurse into. Built lazily on first encounter.
	planCache map[reflect.Type]typePlan
	mu        sync.RWMutex
}

// New compiles rules into a Replacer. Rules with an empty Find are skipped.
// Because Find is quoted via regexp.QuoteMeta, compilation cannot fail on the
// pattern itself — New only returns an error for defensive consistency.
func New(rs Rules) (*Replacer, error) {
	rules := make([]compiledRule, 0, len(rs.Rules))
	for _, r := range rs.Rules {
		if r.Find == "" {
			continue
		}
		pattern := regexp.QuoteMeta(r.Find)
		if !r.CaseSensitive {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("compile scrub rule %q: %w", r.Find, err)
		}
		rules = append(rules, compiledRule{re: re, repl: []byte(r.Replace)})
	}
	return &Replacer{
		rules:     rules,
		planCache: make(map[reflect.Type]typePlan),
	}, nil
}

// Load reads a scrub-rules YAML file (with ${ENV} expansion via config.LoadYAML)
// and compiles it.
func Load(path string) (*Replacer, error) {
	var rs Rules
	if err := config.LoadYAML(path, &rs); err != nil {
		return nil, fmt.Errorf("load scrub rules %s: %w", path, err)
	}
	return New(rs)
}

// Len returns the number of compiled rules.
func (r *Replacer) Len() int { return len(r.rules) }

// ApplyString returns s with all rules applied in order. Non-matching input is
// returned as-is with no allocation: each rule gates on FindStringIndex first,
// and ReplaceAllLiteralString is only reached on a match.
func (r *Replacer) ApplyString(s string) string {
	if len(r.rules) == 0 || s == "" {
		return s
	}
	for _, cr := range r.rules {
		if cr.re.FindStringIndex(s) == nil {
			continue
		}
		s = cr.re.ReplaceAllLiteralString(s, string(cr.repl))
	}
	return s
}

// applyBytes returns b with all rules applied, used for json.RawMessage fields.
// The bool reports whether any rule changed b (so callers skip SetBytes when
// nothing matched). On no match the original slice is returned untouched.
func (r *Replacer) applyBytes(b []byte) ([]byte, bool) {
	if len(r.rules) == 0 || len(b) == 0 {
		return b, false
	}
	changed := false
	for _, cr := range r.rules {
		if cr.re.Find(b) == nil {
			continue
		}
		b = cr.re.ReplaceAllLiteral(b, cr.repl)
		changed = true
	}
	return b, changed
}
