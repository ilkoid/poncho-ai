package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQuoteIdent(t *testing.T) {
	assert.Equal(t, `"cards"`, quoteIdent("cards"))
	assert.Equal(t, `"a""b"`, quoteIdent(`a"b`))           // embedded quote is doubled
	assert.Equal(t, `"t"")--"`, quoteIdent(`t")--`))        // injection attempt is neutralized
	assert.Equal(t, `""`, quoteIdent(""))                   // empty -> ""
}

// TestEscapeRegex — escaped pattern matches the literal text but is NOT
// interpreted as a regex (the whole reason case-insensitive matching needs this).
func TestEscapeRegex(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		mustMatch    string // escaped pattern must match this
		mustNotMatch string // ...and must NOT match this (would match if unescaped)
	}{
		{"dot", "a.b", "a.b", "axb"},     // unescaped '.' would match 'axb'
		{"plus", "a+", "a+", "aaa"},      // unescaped '+' would match 'aaa'
		{"parens", "a(x)", "a(x)", "ax"}, // unescaped group would match 'ax'
		{"brackets", "a[1]", "a[1]", "a1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			re := regexp.MustCompile("(?i)" + escapeRegex(tc.input))
			assert.True(t, re.MatchString(tc.mustMatch), "should match literal %q", tc.mustMatch)
			assert.False(t, re.MatchString(tc.mustNotMatch), "should NOT match %q (escaping failed)", tc.mustNotMatch)
		})
	}
}

// TestEscapeRegex_PlainPassesThrough — no metachars → unchanged.
func TestEscapeRegex_PlainPassesThrough(t *testing.T) {
	assert.Equal(t, "PlayToday", escapeRegex("PlayToday"))
	assert.Equal(t, "Бренд Х", escapeRegex("Бренд Х"))
}

// TestEscapeReplacement — every backslash is doubled so '\1' is not a backref.
func TestEscapeReplacement(t *testing.T) {
	assert.Equal(t, "abc", escapeReplacement("abc"))
	assert.Equal(t, `a\\b`, escapeReplacement(`a\b`))
	assert.Equal(t, `x\\1y`, escapeReplacement(`x\1y`)) // backref-like stays literal
}

func TestParseSelectTables(t *testing.T) {
	assert.Nil(t, parseSelectTables(""))
	assert.Nil(t, parseSelectTables("   "))
	assert.Nil(t, parseSelectTables(" , , "))

	assert.Equal(t, []string{"cards"}, parseSelectTables("cards"))
	assert.Equal(t, []string{"cards"}, parseSelectTables("  cards "))

	assert.Equal(t, []string{"cards", "products"}, parseSelectTables("cards,products"))
	assert.Equal(t, []string{"cards", "products"}, parseSelectTables(" cards , products , "))
}

func sampleTargets() []Target {
	return []Target{
		{Table: "cards", Column: "name", Type: "text"},
		{Table: "cards", Column: "brand", Type: "text"},
		{Table: "products", Column: "title", Type: "text"},
		{Table: "feedbacks", Column: "text", Type: "text"},
	}
}

func TestFilterTables(t *testing.T) {
	all := sampleTargets()

	// No filters → everything.
	got := filterTables(all, nil, nil, nil)
	assert.Len(t, got, 4)

	// select_tables only.
	got = filterTables(all, nil, []string{"cards"}, nil)
	assert.Len(t, got, 2)
	for _, tg := range got {
		assert.Equal(t, "cards", tg.Table)
	}

	// include only.
	got = filterTables(all, []string{"cards", "products"}, nil, nil)
	assert.Len(t, got, 3)

	// include ∩ select — disjoint → empty.
	got = filterTables(all, []string{"cards"}, []string{"products"}, nil)
	assert.Empty(t, got)

	// exclude.
	got = filterTables(all, nil, nil, []string{"cards"})
	assert.Len(t, got, 2)
	for _, tg := range got {
		assert.NotEqual(t, "cards", tg.Table)
	}

	// exclude combined with select.
	got = filterTables(all, nil, []string{"cards", "products"}, []string{"cards"})
	assert.Len(t, got, 1)
	assert.Equal(t, "products", got[0].Table)
}

// --- consolidated per-table SQL (Phase 2) ---

// TestCountTableSQL_Regex — one scan, per-column counts via FILTER, regex predicate.
func TestCountTableSQL_Regex(t *testing.T) {
	s := &Scrubber{schema: "public"}
	g := TableGroup{Table: "cards", Cols: []Target{
		{Table: "cards", Column: "brand", Type: "text"},
		{Table: "cards", Column: "title", Type: "text"},
	}}
	sql := s.countTableSQL(g)
	assert.Contains(t, sql, `count(*) FILTER (WHERE "brand" ~* $1) AS c0`)
	assert.Contains(t, sql, `count(*) FILTER (WHERE "title" ~* $1) AS c1`)
	assert.Contains(t, sql, `FROM "public"."cards"`)
	assert.Equal(t, 1, strings.Count(sql, "FROM"), "consolidated count scans the table once")
}

// TestCountTableSQL_Plain — ILIKE predicate when read is a plain literal.
func TestCountTableSQL_Plain(t *testing.T) {
	s := &Scrubber{schema: "public", plain: true}
	g := TableGroup{Table: "cards", Cols: []Target{{Table: "cards", Column: "brand", Type: "text"}}}
	sql := s.countTableSQL(g)
	assert.Contains(t, sql, `"brand" ILIKE $1`)
	assert.NotContains(t, sql, "~*")
}

// TestUpdateTableSQL_Regex — single UPDATE, CASE-gated replace, WHERE OR-chain.
func TestUpdateTableSQL_Regex(t *testing.T) {
	s := &Scrubber{schema: "public"}
	g := TableGroup{Table: "cards", Cols: []Target{{Table: "cards", Column: "brand", Type: "text"}}}
	sql := s.updateTableSQL(g)
	assert.Contains(t, sql, `UPDATE "public"."cards" SET`)
	assert.Contains(t, sql, `"brand" = CASE WHEN "brand" ~* $1 THEN regexp_replace("brand", $1, $2, 'gi') ELSE "brand" END`)
	assert.Contains(t, sql, `WHERE "brand" ~* $1`)
}

// TestUpdateTableSQL_JSONB — jsonb cast both ways inside the CASE.
func TestUpdateTableSQL_JSONB(t *testing.T) {
	s := &Scrubber{schema: "public"}
	g := TableGroup{Table: "cards", Cols: []Target{{Table: "cards", Column: "meta", Type: "jsonb"}}}
	sql := s.updateTableSQL(g)
	assert.Contains(t, sql, `regexp_replace("meta"::text, $1, $2, 'gi')::jsonb`)
	assert.Contains(t, sql, `"meta"::text ~* $1`)
	assert.Contains(t, sql, `ELSE "meta"`)
}

// TestUpdateTableSQL_Plain_MultiColumn — predicate uses $3 (ilike), regexp_replace keeps $1/$2.
func TestUpdateTableSQL_Plain_MultiColumn(t *testing.T) {
	s := &Scrubber{schema: "public", plain: true}
	g := TableGroup{Table: "cards", Cols: []Target{
		{Table: "cards", Column: "brand", Type: "text"},
		{Table: "cards", Column: "title", Type: "text"},
	}}
	sql := s.updateTableSQL(g)
	assert.Contains(t, sql, `CASE WHEN "brand" ILIKE $3 THEN regexp_replace("brand", $1, $2, 'gi') ELSE "brand" END`)
	assert.Contains(t, sql, `WHERE "brand" ILIKE $3 OR "title" ILIKE $3`)
	assert.NotContains(t, sql, "~*")
}

// TestArgs — arg slices match placeholder count per mode (pgx v5 is strict).
func TestArgs(t *testing.T) {
	s := &Scrubber{schema: "public", plain: false, pattern: "p", replacement: "r", ilike: "%p%"}
	assert.Equal(t, []any{"p"}, s.countArgs())
	assert.Equal(t, []any{"p", "r"}, s.updateArgs())

	s.plain = true
	assert.Equal(t, []any{"%p%"}, s.countArgs())
	assert.Equal(t, []any{"p", "r", "%p%"}, s.updateArgs())
}

func TestEscapeILIKE(t *testing.T) {
	assert.Equal(t, `a\%b`, escapeILIKE("a%b"))
	assert.Equal(t, `a\_b`, escapeILIKE("a_b"))
	assert.Equal(t, `a\\b`, escapeILIKE(`a\b`))
	assert.Equal(t, "PlayToday", escapeILIKE("PlayToday")) // no metachars → unchanged
	assert.Equal(t, `Бренд\_\%`, escapeILIKE("Бренд_%"))
}

func TestIsPlainLiteral(t *testing.T) {
	assert.True(t, isPlainLiteral("PlayToday"))
	assert.True(t, isPlainLiteral("Бренд Х"))
	assert.False(t, isPlainLiteral("Play.Today")) // '.' is a regex metachar
	assert.False(t, isPlainLiteral("a+b"))
	assert.False(t, isPlainLiteral("brand(x)"))
}

func TestGroupTargetsByTable(t *testing.T) {
	targets := []Target{
		{Table: "cards", Column: "brand"},
		{Table: "cards", Column: "title"},
		{Table: "feedbacks", Column: "text"},
	}
	groups := groupTargetsByTable(targets)
	assert.Len(t, groups, 2)
	assert.Equal(t, "cards", groups[0].Table)
	assert.Len(t, groups[0].Cols, 2)
	assert.Equal(t, "brand", groups[0].Cols[0].Column)
	assert.Equal(t, "title", groups[0].Cols[1].Column)
	assert.Equal(t, "feedbacks", groups[1].Table)
	assert.Len(t, groups[1].Cols, 1)
}
