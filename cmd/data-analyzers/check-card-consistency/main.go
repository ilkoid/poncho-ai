package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
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
	Analysis AnalysisConfig `yaml:"analysis"`
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
	AllowedYears   []int    `yaml:"allowed_years"`
	Season         string   `yaml:"season"`
	Subject        string   `yaml:"subject"`
	SubjectID      int      `yaml:"subject_id"`
	VendorCodes    []string `yaml:"vendor_codes"`    // артикулы продавца (8 симв, напр. "12623034")
	NmIDs          []int    `yaml:"nm_ids"`           // артикулы WB (число, напр. 739959074)
	ExcludeLengths []int    `yaml:"exclude_lengths"`
}

type AnalysisConfig struct {
	Concurrency int `yaml:"concurrency"`
	Limit       int `yaml:"limit"`
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
	stage := flag.Int("stage", 1, "Pipeline stage: 1=text, 3=vision, 4=generate, 5=update")
	limit := flag.Int("limit", 0, "Limit number of cards (0=all)")
	mock := flag.Bool("mock", false, "Stage 5: mock mode (no real WB API call)")
	yes := flag.Bool("yes", false, "Stage 5: confirm real WB update")
	listSubjects := flag.String("list-subjects", "", "List WB subjects (empty=all, or search query)")
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

	// Open results DB (read-write)
	results, err := NewResultsRepo(cfg.Results.DBPath)
	if err != nil {
		log.Fatalf("Open results: %v", err)
	}
	defer results.Close()

	// Init schema
	if err := results.InitSchema(ctx); err != nil {
		log.Fatalf("Init schema: %v", err)
	}

	// Route to stage
	stageStart := time.Now()
	switch *stage {
	case 1:
		provider, err := createProvider(cfg)
		if err != nil {
			log.Fatalf("Create LLM provider: %v", err)
		}
		if err := runStage1(ctx, source, results, provider, cfg); err != nil {
			log.Fatalf("Stage 1: %v", err)
		}

	case 3:
		provider, err := createVisionProvider(cfg)
		if err != nil {
			log.Fatalf("Create Vision provider: %v", err)
		}
		if err := runStage3(ctx, source, results, provider, cfg); err != nil {
			log.Fatalf("Stage 3: %v", err)
		}

	case 4:
		provider, err := createProvider(cfg)
		if err != nil {
			log.Fatalf("Create LLM provider: %v", err)
		}
		if err := runStage4(ctx, source, results, provider, cfg); err != nil {
			log.Fatalf("Stage 4: %v", err)
		}

	case 5:
		if !*mock && !*yes {
			log.Fatal("Stage 5 requires --mock (dry run) or --yes (real update)")
		}
		if err := runStage5(ctx, results, cfg, *mock); err != nil {
			log.Fatalf("Stage 5: %v", err)
		}

	default:
		log.Fatalf("Unknown stage: %d (use 1, 3, 4, or 5)", *stage)
	}
	stageDuration := time.Since(stageStart)

	// Print stats
	printStats(ctx, results, *stage, stageDuration)
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
