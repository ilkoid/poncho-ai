package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
)

// FilterStep records one filtering pass and how many cards it excluded.
type FilterStep struct {
	Name     string
	Input    int
	Output   int
	Excluded int
}

// CardPreview holds computed data for one card in the preview.
type CardPreview struct {
	NmID           int
	VendorCode     string
	Title          string
	SubjectID      int
	SubjectName    string
	ProductRating  float64
	FeedbackRating float64
	MaxVisibility  float64
	PriorityScore  float64
	OpenCard30d    int
	Orders30d      int
	TopQuery       string
	PhotoCount     int
	Problems       []string
}

// runStage0Preview выполняет предварительный анализ без записи в БД и без LLM.
func runStage0Preview(ctx context.Context, source *SourceRepo, cfg CLIConfig, charDictPath string) error {
	var steps []FilterStep
	f := cfg.Filter

	// Step 1: total cards in DB
	totalInDB, _ := source.CountCards(ctx)
	steps = append(steps, FilterStep{Name: "total cards in DB", Input: 0, Output: totalInDB})

	// Step 2: base selection (year/vendor_codes/nm_ids — mutually exclusive)
	prevCount := totalInDB
	baseFilter := FilterConfig{
		NmIDs:       f.NmIDs,
		VendorCodes: f.VendorCodes,
		AllowedYears: f.AllowedYears,
	}
	if len(baseFilter.NmIDs) > 0 || len(baseFilter.VendorCodes) > 0 || len(baseFilter.AllowedYears) > 0 {
		n, err := source.CountCardsFiltered(ctx, baseFilter)
		if err != nil {
			return fmt.Errorf("count base: %w", err)
		}
		label := describeBaseFilter(baseFilter)
		steps = append(steps, FilterStep{Name: label, Input: prevCount, Output: n, Excluded: prevCount - n})
		prevCount = n
	}

	// Step 3: exclude_lengths
	if len(f.ExcludeLengths) > 0 {
		elFilter := baseFilter
		elFilter.ExcludeLengths = f.ExcludeLengths
		n, err := source.CountCardsFiltered(ctx, elFilter)
		if err != nil {
			return fmt.Errorf("count exclude_lengths: %w", err)
		}
		steps = append(steps, FilterStep{
			Name: fmt.Sprintf("exclude_lengths %v", f.ExcludeLengths),
			Input: prevCount, Output: n, Excluded: prevCount - n,
		})
		prevCount = n
	}

	// Step 3b: seasons
	if len(f.Seasons) > 0 {
		seasonFilter := baseFilter
		seasonFilter.ExcludeLengths = f.ExcludeLengths
		seasonFilter.Seasons = f.Seasons
		n, err := source.CountCardsFiltered(ctx, seasonFilter)
		if err != nil {
			return fmt.Errorf("count seasons: %w", err)
		}
		steps = append(steps, FilterStep{
			Name: fmt.Sprintf("seasons %v", f.Seasons),
			Input: prevCount, Output: n, Excluded: prevCount - n,
		})
		prevCount = n
	}

	// Step 4: in_stock
	if f.InStock {
		stockFilter := baseFilter
		stockFilter.ExcludeLengths = f.ExcludeLengths
		stockFilter.Seasons = f.Seasons
		stockFilter.InStock = true
		n, err := source.CountCardsFiltered(ctx, stockFilter)
		if err != nil {
			return fmt.Errorf("count in_stock: %w", err)
		}
		steps = append(steps, FilterStep{
			Name: "in_stock",
			Input: prevCount, Output: n, Excluded: prevCount - n,
		})
		prevCount = n
	}

	// Load all cards with full SQL filter
	cards, err := source.LoadCardsForAnalysis(ctx, cfg.Filter)
	if err != nil {
		return fmt.Errorf("load cards: %w", err)
	}

	// If subject filter was applied but not yet counted
	if f.Subject != "" || len(f.SubjectIDs) > 0 {
		n := len(cards)
		label := describeSubjectFilter(f)
		steps = append(steps, FilterStep{Name: label, Input: prevCount, Output: n, Excluded: prevCount - n})
		prevCount = n
	}

	if len(cards) == 0 {
		printFilterPipeline(steps)
		fmt.Println("\nNo cards found for analysis.")
		return nil
	}

	// Collect nmIDs
	nmIDs := make([]int, len(cards))
	for i, c := range cards {
		nmIDs[i] = c.NmID
	}

	// Step 3: load metrics from source DB (no writes)
	ratings, err := source.LoadRatings(ctx, nmIDs)
	if err != nil {
		log.Printf("WARN: load ratings: %v", err)
	}

	visibility, err := source.LoadVisibility(ctx, nmIDs)
	if err != nil {
		log.Printf("WARN: load visibility: %v", err)
	}

	searchMetrics, err := source.LoadSearchMetrics(ctx, nmIDs)
	if err != nil {
		log.Printf("WARN: load search metrics: %v", err)
	}

	topQueries, err := source.LoadTopSearchQueries(ctx, nmIDs, 1)
	if err != nil {
		log.Printf("WARN: load top queries: %v", err)
	}

	photosMap, err := source.LoadPhotos(ctx, nmIDs, cfg.Vision.PhotosPerCard)
	if err != nil {
		log.Printf("WARN: load photos: %v", err)
	}

	// Build CardPreview slice
	previews := make([]CardPreview, 0, len(cards))
	for _, c := range cards {
		ri := ratings[c.NmID]
		vis := visibility[c.NmID]
		sm := searchMetrics[c.NmID]

		topQ := ""
		if qs := topQueries[c.NmID]; len(qs) > 0 {
			topQ = qs[0].Text
		}

		pScore := computePriorityScore(vis, ri.ProductRating, sm.OpenCard30d)

		previews = append(previews, CardPreview{
			NmID:           c.NmID,
			VendorCode:     c.VendorCode,
			Title:          c.Title,
			SubjectID:      c.SubjectID,
			SubjectName:    c.SubjectName,
			ProductRating:  ri.ProductRating,
			FeedbackRating: ri.FeedbackRating,
			MaxVisibility:  vis,
			PriorityScore:  pScore,
			OpenCard30d:    sm.OpenCard30d,
			Orders30d:      sm.Orders30d,
			TopQuery:       topQ,
			PhotoCount:     len(photosMap[c.NmID]),
		})
	}

	// Step 4: in-memory threshold filtering
	if cfg.Filter.hasThresholds() {
		before := len(previews)
		var kept []CardPreview
		for _, p := range previews {
			if passesThresholds(p, cfg.Filter) {
				kept = append(kept, p)
			}
		}
		previews = kept
		steps = append(steps, FilterStep{
			Name: fmt.Sprintf("thresholds (rating<%.1f, fb<%.1f, vis<%.1f)",
				cfg.Filter.MaxProductRating, cfg.Filter.MaxFeedbackRating, cfg.Filter.MaxVisibility),
			Input: before, Output: len(previews), Excluded: before - len(previews),
		})
	}

	if len(previews) == 0 {
		printFilterPipeline(steps)
		fmt.Println("\nNo cards pass threshold filter.")
		return nil
	}

	// Step 5: limit
	if cfg.Analysis.Limit > 0 && len(previews) > cfg.Analysis.Limit {
		before := len(previews)
		previews = previews[:cfg.Analysis.Limit]
		steps = append(steps, FilterStep{
			Name: fmt.Sprintf("limit (%d)", cfg.Analysis.Limit),
			Input: before, Output: len(previews), Excluded: before - len(previews),
		})
	}

	// Step 6: rule-based problem detection
	charsMap, err := source.LoadCharacteristics(ctx, nmIDsForPreviews(previews))
	if err != nil {
		log.Printf("WARN: load characteristics: %v", err)
	}

	var charDict *CharDictRepo
	if _, err := os.Stat(charDictPath); err == nil {
		charDict, err = NewCharDictRepo(charDictPath)
		if err != nil {
			log.Printf("WARN: open char-dict: %v (skipping required char checks)", err)
		} else {
			defer charDict.Close()
		}
	} else {
		log.Printf("WARN: char-dict not found at %s (skipping required char checks)", charDictPath)
	}

	// Build required chars lookup per subject
	requiredChars := buildRequiredCharsLookup(ctx, charDict, previews)

	// Detect problems
	problemCounts := make(map[string]int)
	for i := range previews {
		chars := charsMap[previews[i].NmID]
		probs := detectProblems(previews[i], chars, requiredChars[previews[i].SubjectID])
		previews[i].Problems = probs
		for _, p := range probs {
			problemCounts[p]++
		}
	}

	// Sort by priority score descending
	sort.Slice(previews, func(i, j int) bool {
		return previews[i].PriorityScore > previews[j].PriorityScore
	})

	// === Print report ===
	printFilterPipeline(steps)
	printSubjectDistribution(previews)
	printProblemSummary(problemCounts, len(previews))
	printPriorityRanking(previews)
	printCostEstimation(previews, cfg)

	return nil
}

// describeBaseFilter returns a human-readable label for the base selection filter.
func describeBaseFilter(f FilterConfig) string {
	if len(f.NmIDs) > 0 {
		return fmt.Sprintf("nm_ids [%d items]", len(f.NmIDs))
	}
	if len(f.VendorCodes) > 0 {
		return fmt.Sprintf("vendor_codes [%d items]", len(f.VendorCodes))
	}
	if len(f.AllowedYears) > 0 {
		return fmt.Sprintf("allowed_years %v", f.AllowedYears)
	}
	return "no base filter"
}

// describeSubjectFilter returns a label for subject filters.
func describeSubjectFilter(f FilterConfig) string {
	var parts []string
	if f.Subject != "" {
		parts = append(parts, fmt.Sprintf("subject=%q", f.Subject))
	}
	if len(f.SubjectIDs) > 0 {
		parts = append(parts, fmt.Sprintf("subject_ids %v", f.SubjectIDs))
	}
	return strings.Join(parts, " + ")
}

// computePriorityScore calculates priority score identical to BackfillMetrics SQL.
func computePriorityScore(visibility, productRating float64, openCard30d int) float64 {
	return (1.0-visibility/100.0)*0.5 +
		(1.0-productRating/10.0)*0.3 +
		math.Min(float64(openCard30d)/100.0, 1.0)*0.2
}

// passesThresholds checks if a card passes all threshold filters (in-memory).
func passesThresholds(p CardPreview, f FilterConfig) bool {
	if f.MaxProductRating > 0 && p.ProductRating > 0 && p.ProductRating >= f.MaxProductRating {
		return false
	}
	if f.MaxFeedbackRating > 0 && p.FeedbackRating > 0 && p.FeedbackRating >= f.MaxFeedbackRating {
		return false
	}
	if f.MaxVisibility > 0 && p.MaxVisibility > 0 && p.MaxVisibility >= f.MaxVisibility {
		return false
	}
	return true
}

// detectProblems runs rule-based checks without LLM.
func detectProblems(p CardPreview, chars []CardChar, requiredCharNames map[int]string) []string {
	var probs []string

	if p.Title == "" {
		probs = append(probs, "empty_title")
	} else if len(p.Title) < 15 {
		probs = append(probs, "short_title")
	}

	if p.PhotoCount == 0 {
		probs = append(probs, "no_photos")
	}

	if p.MaxVisibility == 0 {
		probs = append(probs, "zero_visibility")
	}

	if p.ProductRating > 0 && p.ProductRating < 5.0 {
		probs = append(probs, "low_rating")
	}

	if p.FeedbackRating > 0 && p.FeedbackRating < 4.0 {
		probs = append(probs, "bad_feedback")
	}

	// Check characteristics
	charSet := make(map[int]bool, len(chars))
	for _, c := range chars {
		charSet[c.CharID] = true

		// suspicious_color
		if strings.EqualFold(c.Name, "цвет") {
			val := strings.Trim(strings.ToLower(c.Value), "\"[] ")
			if val == "универсальный" {
				probs = append(probs, "suspicious_color")
			}
		}
	}

	// missing_required_char
	for charcID, name := range requiredCharNames {
		if !charSet[charcID] {
			probs = append(probs, fmt.Sprintf("missing_%s", sanitizeProblemName(name)))
		}
	}

	return probs
}

// buildRequiredCharsLookup loads required characteristics per subject from char-dict.
func buildRequiredCharsLookup(ctx context.Context, charDict *CharDictRepo, previews []CardPreview) map[int]map[int]string {
	result := make(map[int]map[int]string)
	if charDict == nil {
		return result
	}

	// Collect unique subject IDs
	subjectSet := make(map[int]bool)
	for _, p := range previews {
		subjectSet[p.SubjectID] = true
	}

	for sid := range subjectSet {
		entries, err := charDict.LoadCharacteristicsForSubject(ctx, sid)
		if err != nil {
			continue
		}
		required := make(map[int]string)
		for _, e := range entries {
			if e.Required {
				required[e.CharcID] = e.Name
			}
		}
		if len(required) > 0 {
			result[sid] = required
		}
	}
	return result
}

// sanitizeProblemName makes a characteristic name safe for use in problem labels.
func sanitizeProblemName(name string) string {
	s := strings.ReplaceAll(strings.ToLower(name), " ", "_")
	s = strings.ReplaceAll(s, "/", "_")
	return s
}

// nmIDsForPreviews extracts nm_id slice from previews.
func nmIDsForPreviews(previews []CardPreview) []int {
	ids := make([]int, len(previews))
	for i, p := range previews {
		ids[i] = p.NmID
	}
	return ids
}


// === Report rendering functions ===

func printFilterPipeline(steps []FilterStep) {
	fmt.Println("\n=== Stage 0: Card Selection Preview ===")
	fmt.Println("\nFilter Pipeline:")

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  STEP\tINPUT\tOUTPUT\tEXCLUDED")
	fmt.Fprintln(w, "  ────\t─────\t──────\t────────")
	for _, s := range steps {
		input := "-"
		if s.Input > 0 {
			input = fmt.Sprintf("%d", s.Input)
		}
		excl := "-"
		if s.Excluded > 0 {
			excl = fmt.Sprintf("%d", s.Excluded)
		}
		fmt.Fprintf(w, "  %s\t%s\t%d\t%s\n", s.Name, input, s.Output, excl)
	}
	w.Flush()
}

func printSubjectDistribution(previews []CardPreview) {
	// Count by subject
	type subjectCount struct {
		id    int
		name  string
		count int
	}
	counts := make(map[int]*subjectCount)
	for _, p := range previews {
		if _, ok := counts[p.SubjectID]; !ok {
			counts[p.SubjectID] = &subjectCount{id: p.SubjectID, name: p.SubjectName}
		}
		counts[p.SubjectID].count++
	}

	// Sort by count desc
	var sorted []*subjectCount
	for _, v := range counts {
		sorted = append(sorted, v)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})

	fmt.Println("\nSubject Distribution:")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  SUBJECT_ID\tSUBJECT_NAME\tCOUNT\tPCT")
	fmt.Fprintln(w, "  ──────────\t────────────\t─────\t───")
	total := len(previews)
	for _, s := range sorted {
		pct := float64(s.count) * 100 / float64(total)
		fmt.Fprintf(w, "  %d\t%s\t%d\t%.1f%%\n", s.id, s.name, s.count, pct)
	}
	w.Flush()
}

func printProblemSummary(problemCounts map[string]int, totalCards int) {
	fmt.Println("\nProblem Detection (rule-based, no LLM):")

	if len(problemCounts) == 0 {
		fmt.Println("  No problems detected.")
		return
	}

	// Sort by count desc
	type pc struct {
		name  string
		count int
	}
	var sorted []pc
	for name, count := range problemCounts {
		sorted = append(sorted, pc{name, count})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  PROBLEM\tCOUNT\tPCT")
	fmt.Fprintln(w, "  ───────\t─────\t───")
	for _, s := range sorted {
		pct := float64(s.count) * 100 / float64(totalCards)
		fmt.Fprintf(w, "  %s\t%d\t%.1f%%\n", s.name, s.count, pct)
	}

	cardsWithProblems := 0
	// Count unique cards with problems (approximation: if any problem count > 0)
	// This is already the total of problem instances, not unique cards.
	// For a rough "cards with problems" we'd need to track during detection.
	_ = cardsWithProblems
	w.Flush()
}

func printPriorityRanking(previews []CardPreview) {
	limit := 30
	if len(previews) < limit {
		limit = len(previews)
	}

	fmt.Printf("\nTop %d Cards by Priority Score:\n", limit)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  #\tNM_ID\tVENDOR\tSUBJECT\tPRIORITY\tVIS%\tRATING\tPHOTOS\tPROBLEMS")
	fmt.Fprintln(w, "  ─\t─────\t──────\t───────\t────────\t────\t──────\t──────\t────────")

	for i := 0; i < limit; i++ {
		p := previews[i]
		probs := "-"
		if len(p.Problems) > 0 {
			// Show first 2 problems
			show := p.Problems
			if len(show) > 2 {
				show = show[:2]
				show = append(show, fmt.Sprintf("+%d", len(p.Problems)-2))
			}
			probs = strings.Join(show, ", ")
		}
		fmt.Fprintf(w, "  %d\t%d\t%s\t%s\t%.2f\t%.1f\t%.1f\t%d\t%s\n",
			i+1, p.NmID, p.VendorCode, truncateStr(p.SubjectName, 25),
			p.PriorityScore, p.MaxVisibility, p.ProductRating, p.PhotoCount, probs)
	}
	w.Flush()
}

func printCostEstimation(previews []CardPreview, cfg CLIConfig) {
	n := len(previews)
	photosPerCard := cfg.Vision.PhotosPerCard
	totalPhotos := n * photosPerCard

	// Estimate tokens per card
	// System prompt: ~800 tokens, User prompt (text+chars): ~500 tokens
	// Photos: ~2500 tokens per image for Gemini Flash
	textTokensPerCard := 1500
	photoTokensPerImage := 2500
	outputTokensPerCard := 500

	totalInputTokens := n * (textTokensPerCard + photosPerCard*photoTokensPerImage)
	totalOutputTokens := n * outputTokensPerCard

	fmt.Println("\nCost Estimation for Stage 1 (Audit):")
	fmt.Printf("  Cards to process:       %d\n", n)
	fmt.Printf("  Photos per card:        %d\n", photosPerCard)
	fmt.Printf("  Total images:           %d\n", totalPhotos)
	fmt.Printf("  Est. input tokens/card: ~%d (text) + ~%d/photo\n", textTokensPerCard, photoTokensPerImage)
	fmt.Printf("  Est. output tokens/card: ~%d\n", outputTokensPerCard)
	fmt.Printf("  Total est. input tokens:  ~%s\n", formatNumber(totalInputTokens))
	fmt.Printf("  Total est. output tokens: ~%s\n", formatNumber(totalOutputTokens))
	fmt.Printf("  Model:                  %s\n", cfg.Vision.Model)
	fmt.Println("  NOTE: Token estimates are approximate. Actual costs depend on prompt length and photo complexity.")
}

func truncateStr(s string, max int) string {
	// Truncate by rune count, not byte count
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}

func formatNumber(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}
