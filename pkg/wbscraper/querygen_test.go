package wbscraper

import (
	"context"
	"errors"
	"net/url"
	"testing"
)

// generator produces the full target list for a constructor config.
func generator(c ConstructorConfig) *StaticGenerator { return NewStaticGenerator(c) }

// TestStaticGeneratorCartesian verifies the nested product (subjects × gender ×
// season × age) with one subject, exercising the "" skip token across dimensions:
//
//	subject="кроссовки", gender=["для мальчика",""], season=["зима",""], age=[""]
//
// → 4 distinct queries. The subject is always present; "" contributes nothing.
func TestStaticGeneratorCartesian(t *testing.T) {
	targets, err := generator(ConstructorConfig{
		Subjects: []string{"кроссовки"},
		Gender:   []string{"для мальчика", ""},
		Season:   []string{"зима", ""},
		Age:      []string{""},
	}).Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	want := []string{
		"кроссовки для мальчика зима",
		"кроссовки для мальчика",
		"кроссовки зима",
		"кроссовки",
	}
	if len(targets) != len(want) {
		t.Fatalf("len(targets) = %d, want %d (%+v)", len(targets), len(want), queryTexts(targets))
	}
	for i, tg := range targets {
		if tg.Query != want[i] {
			t.Errorf("targets[%d].Query = %q, want %q", i, tg.Query, want[i])
		}
		if tg.Kind != "search" {
			t.Errorf("targets[%d].Kind = %q, want search", i, tg.Kind)
		}
	}
}

// TestStaticGeneratorDedup verifies that duplicate query strings collapse to one
// target when Dedup is true, and survive when false. The realistic collision:
// "кроссовки" × age "детские" and "кроссовки детские" × age "" both spell
// "кроссовки детские" — same text, different attribute provenance.
func TestStaticGeneratorDedup(t *testing.T) {
	c := ConstructorConfig{
		Subjects: []string{"кроссовки", "кроссовки детские"},
		Gender:   []string{""},
		Season:   []string{""},
		Age:      []string{"", "детские"},
	}

	t.Run("dedup_on", func(t *testing.T) {
		c.Dedup = true
		targets, err := generator(c).Generate(context.Background())
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		// 4 raw cells, 1 collision ("кроссовки детские") → 3 distinct.
		if len(targets) != 3 {
			t.Fatalf("dedup=true: len = %d, want 3 (%+v)", len(targets), queryTexts(targets))
		}
		// The first occurrence (subject="кроссовки", age="детские") is kept.
		var provenance string
		for _, tg := range targets {
			if tg.Query == "кроссовки детские" {
				provenance = tg.Subject + "/" + tg.Age
			}
		}
		if provenance != "кроссовки/детские" {
			t.Errorf("dedup kept wrong provenance: %q, want кроссовки/детские", provenance)
		}
	})

	t.Run("dedup_off", func(t *testing.T) {
		c.Dedup = false
		targets, err := generator(c).Generate(context.Background())
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if len(targets) != 4 {
			t.Fatalf("dedup=false: len = %d, want 4 (%+v)", len(targets), queryTexts(targets))
		}
	})
}

// TestStaticGeneratorMaxQueries verifies the post-dedup explosion cap.
func TestStaticGeneratorMaxQueries(t *testing.T) {
	c := ConstructorConfig{
		Subjects:   []string{"a", "b", "c", "d"},
		Gender:     []string{""},
		Season:     []string{""},
		Age:        []string{""},
		MaxQueries: 2,
	}
	targets, err := generator(c).Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("len = %d, want 2 (cap)", len(targets))
	}
	if targets[0].Query != "a" || targets[1].Query != "b" {
		t.Errorf("capped targets = %+v, want [a,b]", queryTexts(targets))
	}
	// MaxQueries <= 0 means unlimited: all 4 survive.
	c.MaxQueries = 0
	targets, _ = generator(c).Generate(context.Background())
	if len(targets) != 4 {
		t.Errorf("MaxQueries=0: len = %d, want 4", len(targets))
	}
}

// TestStaticGeneratorURL verifies the WB search URL with correct encoding.
// The query is round-tripped through net/url.QueryEscape and back, matching
// extensions/wb-scraper/src/popup.js.
func TestStaticGeneratorURL(t *testing.T) {
	const q = "кроссовки для мальчика"
	targets, err := generator(ConstructorConfig{
		Subjects: []string{q},
	}).Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("len = %d, want 1", len(targets))
	}
	wantURL := wbSearchURLBase + url.QueryEscape(q)
	if targets[0].URL != wantURL {
		t.Errorf("URL = %q, want %q", targets[0].URL, wantURL)
	}
	// Sanity: the encoded query decodes back to the original text.
	enc := targets[0].URL[len(wbSearchURLBase):]
	if dec, err := url.QueryUnescape(enc); err != nil || dec != q {
		t.Errorf("URL decoded = %q (err %v), want %q", dec, err, q)
	}
}

// TestStaticGeneratorQueryIDIsNoQuery verifies the generator stays DB-free: every
// emitted Target has QueryID == NoQuery; the server is responsible for stamping ids.
func TestStaticGeneratorQueryIDIsNoQuery(t *testing.T) {
	targets, err := generator(ConstructorConfig{
		Subjects: []string{"a", "b"},
	}).Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for i, tg := range targets {
		if tg.QueryID != NoQuery {
			t.Errorf("targets[%d].QueryID = %d, want NoQuery (0); generator must not stamp ids", i, tg.QueryID)
		}
	}
}

// TestStaticGeneratorEmptyDimensions verifies that unconfigured dimensions collapse
// to subject-only queries rather than zeroing out the whole product.
func TestStaticGeneratorEmptyDimensions(t *testing.T) {
	targets, err := generator(ConstructorConfig{
		Subjects: []string{"бейсболки", "рюкзаки"},
		// Gender/Season/Age nil → treated as a single "" axis.
	}).Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("len = %d, want 2 subject-only targets", len(targets))
	}
	if targets[0].Query != "бейсболки" || targets[1].Query != "рюкзаки" {
		t.Errorf("targets = %+v", queryTexts(targets))
	}
}

// TestStaticGeneratorEmptySubjects verifies a degenerate config (no subjects) yields
// no targets rather than erroring — the server treats an empty queue as immediate done.
func TestStaticGeneratorEmptySubjects(t *testing.T) {
	targets, err := generator(ConstructorConfig{}).Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(targets) != 0 {
		t.Errorf("len = %d, want 0 for empty subjects", len(targets))
	}
}

// TestLLMGeneratorStub verifies the future-path generator reports not-implemented
// clearly (not nil/empty) until the pkg/llm integration phase.
func TestLLMGeneratorStub(t *testing.T) {
	var gen QueryGenerator = NewLLMGenerator()
	targets, err := gen.Generate(context.Background())
	if !errors.Is(err, ErrGeneratorNotImplemented) {
		t.Fatalf("err = %v, want ErrGeneratorNotImplemented", err)
	}
	if targets != nil {
		t.Errorf("targets = %+v, want nil", targets)
	}
}

// TestStaticGeneratorSatisfiesInterface is a compile-time guard: the concrete
// generator must satisfy QueryGenerator (so the CLI switch can assign it).
func TestStaticGeneratorSatisfiesInterface(t *testing.T) {
	var _ QueryGenerator = (*StaticGenerator)(nil)
}

// TestDiscardWriterUpsertQueryIdempotent verifies the mock assigns synthetic ids
// that are stable per query text (mirroring a real UNIQUE constraint), so --mock
// mode can exercise the server's query-stamping logic without a DB.
func TestDiscardWriterUpsertQueryIdempotent(t *testing.T) {
	w := NewDiscardWriter()
	ctx := context.Background()

	id1, err := w.UpsertQuery(ctx, SearchQuery{Query: "кроссовки"})
	if err != nil || id1 == NoQuery {
		t.Fatalf("first UpsertQuery: id=%d err=%v (want non-zero, no err)", id1, err)
	}
	// Same text → same id (idempotent, as across sessions).
	id2, err := w.UpsertQuery(ctx, SearchQuery{Query: "кроссовки"})
	if err != nil || id2 != id1 {
		t.Fatalf("repeat UpsertQuery: id=%d err=%v, want %d", id2, err, id1)
	}
	// Different text → different id.
	id3, err := w.UpsertQuery(ctx, SearchQuery{Query: "рюкзаки"})
	if err != nil || id3 == id1 || id3 == NoQuery {
		t.Fatalf("second distinct UpsertQuery: id=%d err=%v, want distinct non-zero", id3, err)
	}
	if got := w.SavedQueries(); got != 2 {
		t.Errorf("SavedQueries = %d, want 2", got)
	}
}

// TestDiscardWriterSatisfiesInterface is a compile-time guard.
func TestDiscardWriterSatisfiesInterface(t *testing.T) {
	var _ Writer = (*DiscardWriter)(nil)
}

// queryTexts is a helper for readable failure output.
func queryTexts(ts []Target) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Query
	}
	return out
}
