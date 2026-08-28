package wb

import "strings"

// IsTemporarilyDisabled reports whether err is a WB API 404 whose body says the
// method is "temporarily disabled" (WB switches endpoints off server-side,
// e.g. supplies-api /api/v1/warehouses + /api/v1/transit-tariffs since
// 2026-08-15, release-notes?id=570). Works through %w wrapping because it
// matches on the rendered error string.
//
// Callers downloading slowly-changing reference data may treat this as
// non-fatal and keep previously stored rows until WB re-enables the method.
func IsTemporarilyDisabled(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "status 404") && strings.Contains(msg, "temporarily disabled")
}
