package scrub

import (
	"encoding/json"
	"reflect"
)

// rawMsgType is reflect.Type for json.RawMessage ([]byte). A field of exactly
// this type is scrubbed as JSON text (the characteristic-value case); a plain
// []byte field is left untouched.
var rawMsgType = reflect.TypeOf(json.RawMessage(nil))

// typePlan memoizes the scrub-relevant field indices for a struct type.
// Indices are relative to the type itself (FieldByIndex paths). Built lazily by
// Replacer.plan on first encounter of the type.
type typePlan struct {
	stringFields  [][]int // exported string fields → ApplyString
	rawMsgFields  [][]int // exported json.RawMessage fields → applyBytes
	recurseFields [][]int // exported struct/slice/array/map/ptr fields → descend
}

// empty reports whether the type has nothing to scrub or recurse into.
func (p typePlan) empty() bool {
	return len(p.stringFields) == 0 && len(p.rawMsgFields) == 0 && len(p.recurseFields) == 0
}

// plan returns the cached typePlan for t, building it on first encounter.
// The plan is shallow (direct fields only); nested types get their own plan
// when applyValue descends into them.
func (r *Replacer) plan(t reflect.Type) typePlan {
	r.mu.RLock()
	if p, ok := r.planCache[t]; ok {
		r.mu.RUnlock()
		return p
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.planCache[t]; ok { // double-check under write lock
		return p
	}
	p := r.buildPlan(t)
	r.planCache[t] = p
	return p
}

// buildPlan classifies the exported fields of t into scrub targets and recurse
// targets. pkg/wb DTOs use flat named fields, so field index paths are
// single-element; embedded/anonymous fields are handled generically by f.Index.
func (r *Replacer) buildPlan(t reflect.Type) typePlan {
	var p typePlan
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		switch {
		case f.Type == rawMsgType:
			p.rawMsgFields = append(p.rawMsgFields, f.Index)
		case f.Type.Kind() == reflect.String:
			p.stringFields = append(p.stringFields, f.Index)
		case needsRecurse(f.Type):
			p.recurseFields = append(p.recurseFields, f.Index)
		}
	}
	return p
}

// needsRecurse reports whether a field of this type may contain scrub targets.
// Primitive kinds (ints, bools, floats, plain strings, byte slices, time.Time's
// private fields) are excluded.
func needsRecurse(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer:
		ek := t.Elem().Kind()
		return ek == reflect.Struct || ek == reflect.Slice || ek == reflect.Array || ek == reflect.Map || ek == reflect.Pointer
	case reflect.Struct, reflect.Map:
		return true
	case reflect.Slice, reflect.Array:
		return t.Elem().Kind() != reflect.Uint8 // skip []byte (only RawMessage is scrubbed)
	default:
		return false
	}
}

// ApplySlice applies scrubbing in-place to every element of s.
// s must be a slice, an array, or a pointer to a slice/array. Other kinds are
// ignored. This is the primary entry point for downloaders and the only place a
// value is boxed into any (reflect.ValueOf(s)) — all internal recursion stays in
// reflect.Value, so the walk performs zero per-field heap allocation.
func (r *Replacer) ApplySlice(s any) {
	if s == nil || len(r.rules) == 0 {
		return
	}
	r.applyValue(reflect.ValueOf(s))
}

// ApplyStruct applies scrubbing in-place to v. v must be addressable for writes
// to take effect — pass a pointer to a struct, or use ApplySlice on a slice.
// A non-addressable struct value is a no-op (Go does not allow mutating it).
func (r *Replacer) ApplyStruct(v any) {
	if v == nil || len(r.rules) == 0 {
		return
	}
	r.applyValue(reflect.ValueOf(v))
}

// applyValue is the recursive core. It never boxes values into interface{}.
func (r *Replacer) applyValue(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		r.applyValue(v.Elem())
	case reflect.Struct:
		r.applyStruct(v)
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			r.applyValue(v.Index(i)) // slice/array elements are addressable
		}
	case reflect.Map:
		r.applyMap(v)
	}
	// Interface, Chan, Func, basic kinds: nothing to do.
}

// applyMap scrubs string map values in place. Map keys are left intact (keys are
// frequently IDs and re-keying is error-prone). Non-string map values are NOT
// mutated: map values are not addressable in Go, so nested structs inside maps
// cannot be rewritten in place. This is acceptable for pkg/wb DTOs, whose map
// fields hold error metadata rather than scrub-relevant text.
func (r *Replacer) applyMap(v reflect.Value) {
	if v.Type().Elem().Kind() != reflect.String {
		return
	}
	iter := v.MapRange()
	for iter.Next() {
		old := iter.Value().String()
		news := r.ApplyString(old)
		if news != old {
			v.SetMapIndex(iter.Key(), reflect.ValueOf(news))
		}
	}
}

// applyStruct rewrites string and json.RawMessage fields of v in place, then
// recurses into nested fields. Fields that are not settable (v not addressable)
// are skipped via CanSet guards rather than panicking.
func (r *Replacer) applyStruct(v reflect.Value) {
	plan := r.plan(v.Type())
	if plan.empty() {
		return
	}
	for _, idx := range plan.stringFields {
		f := v.FieldByIndex(idx)
		if !f.CanSet() {
			continue
		}
		if news := r.ApplyString(f.String()); news != f.String() {
			f.SetString(news)
		}
	}
	for _, idx := range plan.rawMsgFields {
		f := v.FieldByIndex(idx)
		if !f.CanSet() {
			continue
		}
		newb, changed := r.applyBytes(f.Bytes())
		if changed {
			f.SetBytes(newb)
		}
	}
	for _, idx := range plan.recurseFields {
		r.applyValue(v.FieldByIndex(idx))
	}
}
