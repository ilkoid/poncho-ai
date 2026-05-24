package filter

import (
	"fmt"
	"strings"
)

// extractYear extracts 2-digit year from vendor_code positions 2-3 (1-indexed).
// Returns -1 if vendor_code is shorter than 3 chars.
func extractYear(vc string) int {
	if len(vc) < 3 {
		return -1
	}
	var year int
	fmt.Sscanf(vc[1:3], "%d", &year)
	return year
}

// placeholders generates "?,?,...,?" string for SQL IN clauses.
func placeholders(n int) string {
	p := make([]string, n)
	for i := range p {
		p[i] = "?"
	}
	return strings.Join(p, ",")
}

// intSliceToAny converts []int to []any for SQL args.
func intSliceToAny(s []int) []any {
	a := make([]any, len(s))
	for i, v := range s {
		a[i] = v
	}
	return a
}

// stringSliceToAny converts []string to []any for SQL args.
func stringSliceToAny(s []string) []any {
	a := make([]any, len(s))
	for i, v := range s {
		a[i] = v
	}
	return a
}

// intSet builds a lookup map from a slice.
func intSet(s []int) map[int]bool {
	m := make(map[int]bool, len(s))
	for _, v := range s {
		m[v] = true
	}
	return m
}

// stringSet builds a case-sensitive lookup map from a slice.
func stringSet(s []string) map[string]bool {
	m := make(map[string]bool, len(s))
	for _, v := range s {
		m[v] = true
	}
	return m
}
