package wbscraper

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

// QueryGenerator produces the work queue of Targets the extension will scrape.
// Declared in the consumer package (Rule 6); implementations live alongside it.
//
// The interface is justified, not ceremonial: generator.kind in the config
// selects a strategy at runtime (OCP/Strategy), the LLM variant is a planned
// second implementation, and the interface gives tests a clean seam. The future
// LLM path may stream; for now Generate returns the whole list.
//
// Generators are DB-free by design: they emit text + attributes with QueryID
// left at NoQuery. The server is the authority that upserts each query text into
// search_queries (via Writer.UpsertQuery → stable id by UNIQUE text) and stamps
// that QueryID into the target before serving it. Keeping the id out of the
// generator means a generator never touches storage and a query keeps one id
// across sessions and generators.
type QueryGenerator interface {
	// Generate returns the full target list, deterministically ordered.
	// Targets carry QueryID == NoQuery; the server fills it.
	Generate(ctx context.Context) ([]Target, error)
}

// ErrGeneratorNotImplemented is returned by stub generators that have no real
// implementation yet (LLMGenerator, before the pkg/llm integration phase). Callers
// surface it as a clear "not wired up" error rather than a nil/empty result.
var ErrGeneratorNotImplemented = errors.New("wbscraper: generator not implemented")

// wbSearchURLBase is the WB storefront search endpoint the extension navigates to.
// Mirrors extensions/wb-scraper/src/popup.js (encodeURIComponent of the query).
const wbSearchURLBase = "https://www.wildberries.ru/catalog/0/search.aspx?search="

// wbCardURLBase is the WB storefront card-detail endpoint (per nmId).
const wbCardURLBase = "https://www.wildberries.ru/catalog/"

// SearchURL returns the WB storefront search URL for a query text. Shared by
// StaticGenerator and the CLI's debug-target override so the URL format lives in
// one place (Rule 0): a future change to the storefront URL scheme touches one line.
func SearchURL(query string) string {
	return wbSearchURLBase + url.QueryEscape(query)
}

// CardURL returns the WB storefront card-detail URL for an nmId string. Used by
// the CLI's debug-target override (a numeric Config.Targets entry).
func CardURL(nmID string) string {
	return wbCardURLBase + nmID + "/detail.aspx"
}

// StaticGenerator builds the target queue as the Cartesian product of subjects and
// the attribute dimensions (gender × season × age), per the constructor block of
// Config. This is the current, config-driven generator; LLMGenerator plugs into
// the same interface for the future LLM phase.
//
// Construction rules (see plan § querygen.go):
//   - Subjects is the seed list and the outer axis; every query contains a subject.
//   - The "" token in any dimension list means "skip that dimension for this cell";
//     non-empty tokens are joined with a single space into the query text.
//   - Duplicate query strings are dropped when Dedup is true (default), keeping the
//     FIRST attribute tuple in iteration order (subjects outermost). Dedup is by
//     query text, not by attribute tuple: "кроссовки детские" built two ways counts once.
//   - MaxQueries caps the final (post-dedup) list as an explosion guard.
type StaticGenerator struct {
	constructor ConstructorConfig
}

// NewStaticGenerator builds a generator from the constructor block of Config.
// The generator holds no other state (it is a pure function of the config).
func NewStaticGenerator(c ConstructorConfig) *StaticGenerator {
	return &StaticGenerator{constructor: c}
}

// Generate walks subjects × gender × season × age in that nested order (deterministic)
// and returns one Target per surviving, deduplicated query. An empty subject list
// yields an empty slice (degenerate config — the server treats an empty queue as an
// immediate done). Context is accepted for interface conformance; the product is an
// in-memory computation, so cancellation is not checked.
func (g *StaticGenerator) Generate(_ context.Context) ([]Target, error) {
	c := g.constructor
	seen := make(map[string]struct{})
	var out []Target

	for _, subject := range dimension(c.Subjects) {
		for _, gender := range dimension(c.Gender) {
			for _, season := range dimension(c.Season) {
				for _, age := range dimension(c.Age) {
					query := joinTokens(subject, gender, season, age)
					if query == "" {
						continue // all dimensions empty for this cell — nothing to search
					}
					if c.Dedup {
						if _, dup := seen[query]; dup {
							continue
						}
						seen[query] = struct{}{}
					}
					out = append(out, Target{
						Kind:    "search",
						QueryID: NoQuery, // server stamps the real id via UpsertQuery
						Query:   query,
						URL:     SearchURL(query),
						Subject: subject,
						Gender:  gender,
						Season:  season,
						Age:     age,
					})
				}
			}
		}
	}

	// Explosion guard: cap the deduplicated list. MaxQueries <= 0 means unlimited.
	if c.MaxQueries > 0 && len(out) > c.MaxQueries {
		out = out[:c.MaxQueries]
	}
	return out, nil
}

// LLMGenerator is the placeholder for future LLM-driven query generation. It
// satisfies QueryGenerator so the CLI/server can select it via generator.kind
// ("llm"), but returns ErrGeneratorNotImplemented until the pkg/llm integration
// phase (which will generate queries under a goal through the Provider interface,
// per Rule 4 — no direct API calls in business logic).
type LLMGenerator struct{}

// NewLLMGenerator returns the stub LLM generator.
func NewLLMGenerator() *LLMGenerator { return &LLMGenerator{} }

// Generate reports not-implemented; replaced by the real LLM path in a later phase.
func (LLMGenerator) Generate(context.Context) ([]Target, error) {
	return nil, ErrGeneratorNotImplemented
}

// dimension returns the slice unchanged, or a single "" for an empty/nil list so
// the Cartesian product still iterates once over that axis (the "" token then
// contributes nothing to the query). Without this, an unconfigured dimension would
// collapse the whole product to zero.
func dimension(dim []string) []string {
	if len(dim) == 0 {
		return []string{""}
	}
	return dim
}

// joinTokens joins the non-empty parts with single spaces, preserving order.
// "" is the documented skip token (dimension = absent), so it is dropped here.
func joinTokens(parts ...string) string {
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(p)
	}
	return b.String()
}
