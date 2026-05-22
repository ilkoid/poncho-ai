package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// CLIConfig — конфигурация утилиты из config.yaml.
type CLIConfig struct {
	Brand    string         `yaml:"brand"`
	LLM      LLMConfig      `yaml:"llm"`
	Text     ModelConfig    `yaml:"text"`
	Vision   VisionConfig   `yaml:"vision"`
	Source   SourceConfig   `yaml:"source"`
	Results  ResultsConfig  `yaml:"results"`
	CharDict CharDictConfig `yaml:"char_dict"`
	Filter   FilterConfig   `yaml:"filter"`
	Analysis AnalysisConfig  `yaml:"analysis"`
	Prompts  PromptConfig   `yaml:"prompts"`
	WBUpdate        WBUpdateConfig `yaml:"wb_update"`
	ProtectedCharIDs []int         `yaml:"protected_char_ids"`
}

// AudienceRule — правила генерации title/description для конкретной аудитории.
type AudienceRule struct {
	TitleRules string `yaml:"title_rules"`
	DescRules  string `yaml:"desc_rules"`
	SEOContext  string `yaml:"seo_context"`
}

type PromptConfig struct {
	Stage1System     string                   `yaml:"stage1_system"`
	Stage1User       string                   `yaml:"stage1_user"`
	Stage3System     string                   `yaml:"stage3_system"`
	Stage3User       string                   `yaml:"stage3_user"`
	Stage4SelectSys  string                   `yaml:"stage4_select_system"`
	Stage4SelectUser string                   `yaml:"stage4_select_user"`
	Stage4FillSys    string                   `yaml:"stage4_fill_system"`
	Stage4FillUser   string                   `yaml:"stage4_fill_user"`
	Stage4CharsSys   string                   `yaml:"stage4_chars_system"`
	Stage4CharsUser  string                   `yaml:"stage4_chars_user"`
	AudienceRules    map[string]AudienceRule  `yaml:"audience_rules"`
}

type LLMConfig struct {
	Provider string `yaml:"provider"`
	APIKey   string `yaml:"api_key"`
	BaseURL  string `yaml:"base_url"`
}

type ModelConfig struct {
	Model       string        `yaml:"model"`
	Temperature float64       `yaml:"temperature"`
	MaxTokens   int           `yaml:"max_tokens"`
	Timeout     time.Duration `yaml:"timeout"`
}

type VisionConfig struct {
	ModelConfig   `yaml:",inline"`
	PhotosPerCard int `yaml:"photos_per_card"`
}

type SourceConfig struct {
	DBPath string `yaml:"db_path"`
}

type ResultsConfig struct {
	DBPath string `yaml:"db_path"`
}

type CharDictConfig struct {
	DBPath string `yaml:"db_path"`
}

type FilterConfig struct {
	InStock           bool     `yaml:"in_stock"`
	AllowedYears      []int    `yaml:"allowed_years"`
	Seasons           []string `yaml:"seasons"`
	Subject           string   `yaml:"subject"`
	SubjectIDs        []int    `yaml:"subject_ids"`
	VendorCodes       []string `yaml:"vendor_codes"`
	NmIDs             []int    `yaml:"nm_ids"`
	ExcludeLengths    []int    `yaml:"exclude_lengths"`
	MaxProductRating  float64  `yaml:"max_product_rating"`  // 0=все, иначе product_rating < порога
	MaxFeedbackRating float64  `yaml:"max_feedback_rating"` // 0=все, иначе feedback_rating < порога
	MaxVisibility     float64             `yaml:"max_visibility"`      // 0=все, иначе max_visibility < порога
	Problems          ProblemFilterConfig `yaml:"problems"`
}

// ProblemFilterConfig позволяет запускать скрипт только по проблемным карточкам
type ProblemFilterConfig struct {
	AnyDiscrepancy  bool `yaml:"any_discrepancy"`
	HasParseErrors  bool `yaml:"has_parse_errors"`
	PendingWBUpdate bool `yaml:"pending_wb_update"`
}

func (f FilterConfig) hasThresholds() bool {
	return f.MaxProductRating > 0 || f.MaxFeedbackRating > 0 || f.MaxVisibility > 0
}

type AnalysisConfig struct {
	Concurrency int `yaml:"concurrency"`
	Limit       int `yaml:"limit"`
}

type WBUpdateConfig struct {
	APIKey            string `yaml:"api_key"`
	BatchSize         int    `yaml:"batch_size"`
	RatePerMin         int `yaml:"rate_per_min"`
	RateBurst          int `yaml:"rate_burst"`
	APIFloorPerMin     int `yaml:"api_floor_per_min"`
	APIFloorBurst      int `yaml:"api_floor_burst"`
	AdaptiveProbeAfter int `yaml:"adaptive_probe_after"`
	MaxBackoffSeconds  int `yaml:"max_backoff_seconds"`
}

// toModelDef конвертирует CLIConfig в config.ModelDef для openai.NewClient().
func (c CLIConfig) toModelDef() config.ModelDef {
	return config.ModelDef{
		Provider:    c.LLM.Provider,
		ModelName:   c.Text.Model,
		APIKey:      c.LLM.APIKey,
		BaseURL:     c.LLM.BaseURL,
		Temperature: c.Text.Temperature,
		MaxTokens:   c.Text.MaxTokens,
		Timeout:     c.Text.Timeout,
	}
}

// toVisionModelDef конвертирует CLIConfig в config.ModelDef для Vision модели.
func (c CLIConfig) toVisionModelDef() config.ModelDef {
	return config.ModelDef{
		Provider:    c.LLM.Provider,
		ModelName:   c.Vision.Model,
		APIKey:      c.LLM.APIKey,
		BaseURL:     c.LLM.BaseURL,
		Temperature: c.Vision.Temperature,
		MaxTokens:   c.Vision.MaxTokens,
		Timeout:     c.Vision.Timeout,
		IsVision:    true,
	}
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	stage := flag.Int("stage", 1, "Pipeline stage: 0=preview, 1=audit, 2=metrics, 4=generate, 5=update")
	limit := flag.Int("limit", 0, "Limit number of cards (0=all)")
	mock := flag.Bool("mock", false, "Stage 5: mock mode (no real WB API call)")
	yes := flag.Bool("yes", false, "Stage 5: confirm real WB update")
	diff := flag.Bool("diff", false, "Stage 4: show before/after diff instead of generating")
	full := flag.Bool("full", false, "Stage 4 --diff: show ALL characteristics (filled + empty)")
	listSubjects := flag.String("list-subjects", "", "List WB subjects (empty=all, or search query)")
	listCharcs := flag.Int("list-charcs", 0, "List characteristics for WB subject ID")
	exportPath := flag.String("export", "", "Export card_analysis to XLSX file and exit")
	force := flag.Bool("force", false, "Force re-processing of already completed cards")
	keepSubject := flag.Bool("keep-subject", false, "Stage 4: ignore LLM subject changes, keep current subject")
	check := flag.Bool("check", false, "Stage 5: check WB error list only (no update)")
	flag.Parse()

	// Load config
	var cfg CLIConfig
	if err := config.LoadYAML(*configPath, &cfg); err != nil {
		log.Fatalf("Load config: %v", err)
	}

	// Apply defaults
	if cfg.Analysis.Concurrency == 0 {
		cfg.Analysis.Concurrency = 5
	}
	if cfg.Vision.PhotosPerCard == 0 {
		cfg.Vision.PhotosPerCard = 3
	}
	if cfg.WBUpdate.BatchSize == 0 {
		cfg.WBUpdate.BatchSize = 50
	}
	if cfg.WBUpdate.RatePerMin == 0 {
		cfg.WBUpdate.RatePerMin = 10
	}
	if cfg.WBUpdate.RateBurst == 0 {
		cfg.WBUpdate.RateBurst = 5
	}
	if cfg.WBUpdate.APIFloorPerMin == 0 {
		cfg.WBUpdate.APIFloorPerMin = 10
	}
	if cfg.WBUpdate.APIFloorBurst == 0 {
		cfg.WBUpdate.APIFloorBurst = 5
	}
	if cfg.WBUpdate.AdaptiveProbeAfter == 0 {
		cfg.WBUpdate.AdaptiveProbeAfter = 10
	}
	if cfg.WBUpdate.MaxBackoffSeconds == 0 {
		cfg.WBUpdate.MaxBackoffSeconds = 60
	}

	// CLI overrides
	if *limit > 0 {
		cfg.Analysis.Limit = *limit
	}

	// Context with graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nInterrupted!")
		cancel()
	}()

	// Open source DB (read-only)
	source, err := NewSourceRepo(cfg.Source.DBPath)
	if err != nil {
		log.Fatalf("Open source: %v", err)
	}
	defer source.Close()

	// --list-subjects: вывести предметы WB и выйти
	if *listSubjects != "" {
		printSubjects(ctx, source, *listSubjects)
		return
	}

	// --list-charcs: вывести характеристики предмета WB и выйти
	if *listCharcs > 0 {
		printCharacteristics(ctx, *listCharcs, cfg)
		return
	}

	// --export: выгрузить card_analysis в XLSX и выйти
	if *exportPath != "" {
		results, err := NewResultsRepo(cfg.Results.DBPath)
		if err != nil {
			log.Fatalf("Open results: %v", err)
		}
		defer results.Close()

		if err := results.InitSchema(ctx); err != nil {
			log.Fatalf("Init schema: %v", err)
		}
		if n, err := results.BackfillMetrics(ctx, cfg.Source.DBPath); err != nil {
			log.Printf("WARN: backfill metrics: %v", err)
		} else if n > 0 {
			log.Printf("Backfilled metrics: %d updates", n)
		}

		photoLoader := func(ctx context.Context, nmIDs []int) map[int][]byte {
			urlMap, err := source.LoadThumbnailURLs(ctx, nmIDs)
			if err != nil {
				log.Printf("WARN: load thumbnails: %v", err)
				return nil
			}
			return downloadThumbnails(ctx, urlMap)
		}

		n, err := results.ExportXLSX(ctx, *exportPath, photoLoader, cfg.Filter)
		if err != nil {
			log.Fatalf("Export: %v", err)
		}
		log.Printf("Exported %d rows to %s", n, *exportPath)
		return
	}

	// Open results DB (read-write) — skip for Stage 0 (read-only preview)
	var results *ResultsRepo
	if *stage != 0 {
		results, err = NewResultsRepo(cfg.Results.DBPath)
		if err != nil {
			log.Fatalf("Open results: %v", err)
		}
		defer results.Close()

		if err := results.InitSchema(ctx); err != nil {
			log.Fatalf("Init schema: %v", err)
		}
	}

	// Backfill metrics вызывается теперь внутри стадий (stage1/audit), чтобы не обновлять вхолостую
	// до того как EnsureRows добавит новые карточки.

	// Route to stage
	stageStart := time.Now()
	switch *stage {
	case 0:
		charDictPath := expandHome(cfg.CharDict.DBPath)
		if err := runStage0Preview(ctx, source, cfg, charDictPath); err != nil {
			log.Fatalf("Stage 0: %v", err)
		}

	case 1:
		provider, err := createVisionProvider(cfg) // Единый аудит использует Vision модель
		if err != nil {
			log.Fatalf("Create Vision provider for Audit: %v", err)
		}
		if err := runStage1Audit(ctx, source, results, provider, cfg, *force); err != nil {
			log.Fatalf("Stage 1 Audit: %v", err)
		}

	case 2:
		if err := runStage2Metrics(ctx, source, results, cfg); err != nil {
			log.Fatalf("Stage 2 Metrics: %v", err)
		}

	case 4:
		if *diff || *full {
			if err := runStage4Diff(ctx, source, results, cfg, *full, *force); err != nil {
				log.Fatalf("Stage 4 diff: %v", err)
			}
		} else {
			provider, err := createProvider(cfg)
			if err != nil {
				log.Fatalf("Create LLM provider: %v", err)
			}
			if err := runStage4(ctx, source, results, provider, cfg, *force, *keepSubject); err != nil {
				log.Fatalf("Stage 4: %v", err)
			}
		}

	case 5:
		if *check {
			if err := runStage5Check(ctx, cfg); err != nil {
				log.Fatalf("Stage 5 check: %v", err)
			}
		} else if !*mock && !*yes {
			log.Fatal("Stage 5 requires --mock (dry run), --yes (real update), or --check (error list only)")
		} else {
			if err := runStage5(ctx, source, results, cfg, *mock, *force); err != nil {
				log.Fatalf("Stage 5: %v", err)
			}
		}

	default:
		log.Fatalf("Unknown stage: %d (use 0, 1, 2, 4, or 5)", *stage)
	}
	stageDuration := time.Since(stageStart)

	// Print stats (skip for Stage 0 — no results DB)
	if results != nil {
		printStats(ctx, results, *stage, stageDuration)
	}
}

func printStats(ctx context.Context, results *ResultsRepo, stage int, duration time.Duration) {
	total, textChecked, textDisc, visionChecked, visionDisc, generated, wbUpdated, err := results.Stats(ctx)
	if err != nil {
		log.Printf("WARN: stats: %v", err)
		return
	}

	fmt.Printf("\n")
	fmt.Printf("=== Stage %d Summary ===\n", stage)
	fmt.Printf("Duration:             %s\n", duration.Round(time.Second))
	fmt.Printf("Total cards in DB:    %d\n", total)
	fmt.Printf("Text checked:         %d (discrepancies: %d, %.0f%%)\n", textChecked, textDisc, pct(textDisc, textChecked))
	fmt.Printf("Vision checked:       %d (discrepancies: %d, %.0f%%)\n", visionChecked, visionDisc, pct(visionDisc, visionChecked))
	fmt.Printf("Params generated:     %d\n", generated)
	fmt.Printf("WB updated:           %d\n", wbUpdated)
}

func pct(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) * 100 / float64(total)
}

// printSubjects выводит предметы WB с их ID для использования в subject_id фильтре.
// Если query="all" — выводит все, иначе ищет по подстроке (регистронезависимо).
func printSubjects(ctx context.Context, source *SourceRepo, query string) {
	all, err := source.LoadAllSubjects(ctx)
	if err != nil {
		log.Fatalf("Load subjects: %v", err)
	}

	var subjects []SubjectEntry
	if query == "all" {
		subjects = all
	} else {
		q := strings.ToLower(query)
		for _, s := range all {
			if strings.Contains(strings.ToLower(s.SubjectName), q) {
				subjects = append(subjects, s)
			}
		}
	}

	if len(subjects) == 0 {
		fmt.Printf("No subjects matching %q\n", query)
		return
	}

	fmt.Printf("%-8s  %s\n", "ID", "Subject Name")
	fmt.Printf("%-8s  %s\n", "--------", "------------")
	for _, s := range subjects {
		fmt.Printf("%-8d  %s\n", s.SubjectID, s.SubjectName)
	}
	fmt.Printf("\nTotal: %d subjects\n", len(subjects))
}

// printCharacteristics выводит характеристики предмета WB по его subject_id.
func printCharacteristics(ctx context.Context, subjectID int, cfg CLIConfig) {
	apiKey := getWBApiKey(cfg.WBUpdate.APIKey)
	if apiKey == "" {
		log.Fatal("WB_API_KEY (or WB_API_ANALYTICS_AND_PROMO_KEY) not set")
	}

	client := wb.New(apiKey)
	client.SetRateLimit("cards_content",
		cfg.WBUpdate.RatePerMin, cfg.WBUpdate.RateBurst,
		cfg.WBUpdate.APIFloorPerMin, cfg.WBUpdate.APIFloorBurst)

	charcs, err := client.GetCharacteristics(ctx, wb.CardsBaseURL,
		cfg.WBUpdate.RatePerMin, cfg.WBUpdate.RateBurst, subjectID)
	if err != nil {
		log.Fatalf("GetCharacteristics: %v", err)
	}
	if len(charcs) == 0 {
		fmt.Printf("No characteristics found for subject_id=%d\n", subjectID)
		return
	}

	// Sort: Required DESC → Popular DESC → Name ASC
	sort.Slice(charcs, func(i, j int) bool {
		if charcs[i].Required != charcs[j].Required {
			return charcs[i].Required
		}
		if charcs[i].Popular != charcs[j].Popular {
			return charcs[i].Popular
		}
		return charcs[i].Name < charcs[j].Name
	})

	fmt.Printf("Characteristics for subject_id=%d (%d total)\n\n", subjectID, len(charcs))
	fmt.Printf("%-8s  %-4s  %-6s  %-3s  %-3s  %s\n", "ID", "Type", "Unit", "Req", "Pop", "Name")
	fmt.Printf("%-8s  %-4s  %-6s  %-3s  %-3s  %s\n", "--------", "----", "------", "---", "---", "----")

	var required, popular int
	for _, c := range charcs {
		req := ""
		if c.Required {
			req = "+"
			required++
		}
		pop := ""
		if c.Popular {
			pop = "+"
			popular++
		}
		fmt.Printf("%-8d  %-4d  %-6s  %-3s  %-3s  %s\n",
			c.CharcID, c.CharcType, c.UnitName, req, pop, c.Name)
	}

	fmt.Printf("\nTotal: %d characteristics (required: %d, popular: %d)\n", len(charcs), required, popular)
}

// downloadThumbnails скачивает миниатюры по URL-мапе, конвертирует WebP → JPEG,
// растягивает до targetW×targetH без сохранения пропорций.
// Загрузка выполняется параллельно (maxWorkers горутин).
func downloadThumbnails(ctx context.Context, urlMap map[int]string) map[int][]byte {
	const targetW, targetH = 100, 75
	const maxWorkers = 20
	const jpegQuality = 60

	type photoResult struct {
		nmID int
		data []byte
	}

	result := make(map[int][]byte, len(urlMap))
	client := &http.Client{Timeout: 10 * time.Second}

	sem := make(chan struct{}, maxWorkers)
	ch := make(chan photoResult, len(urlMap))
	var wg sync.WaitGroup

	for nmID, url := range urlMap {
		if url == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(nmID int, url string) {
			defer wg.Done()
			defer func() { <-sem }()

			req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
			if err != nil {
				log.Printf("WARN: create request nm_id=%d: %v", nmID, err)
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				log.Printf("WARN: download photo nm_id=%d: %v", nmID, err)
				return
			}
			if resp.StatusCode != 200 {
				resp.Body.Close()
				log.Printf("WARN: photo nm_id=%d returned status %d", nmID, resp.StatusCode)
				return
			}
			data, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				log.Printf("WARN: read photo nm_id=%d: %v", nmID, err)
				return
			}

			img, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				log.Printf("WARN: decode image nm_id=%d: %v", nmID, err)
				return
			}

			dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
			draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)

			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: jpegQuality}); err != nil {
				log.Printf("WARN: encode jpeg nm_id=%d: %v", nmID, err)
				return
			}
			ch <- photoResult{nmID: nmID, data: buf.Bytes()}
		}(nmID, url)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	for pr := range ch {
		result[pr.nmID] = pr.data
	}
	return result
}
