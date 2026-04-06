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

	_ "github.com/mattn/go-sqlite3"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config wraps configuration for the cards downloader.
type Config struct {
	WB    config.WBClientConfig `yaml:"wb"`
	Cards config.CardsConfig    `yaml:"cards"`
}

func main() {
	// 1. Parse flags
	configPath := flag.String("config", "config.yaml", "путь к конфигу")
	dbPath := flag.String("db", "", "путь к базе (overrides config)")
	resume := flag.Bool("resume", false, "resume from last cursor")
	limit := flag.Int("limit", 0, "max cards to download (0 = unlimited)")
	clean := flag.Bool("clean", false, "clean database before download")
	mockMode := flag.Bool("mock", false, "mock mode (no API calls)")
	help := flag.Bool("help", false, "справка")
	flag.BoolVar(help, "h", false, "справка")
	flag.Parse()

	if *help {
		printHelp()
		return
	}

	// 2. Load config
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Printf("Config not found, using defaults: %v", err)
		cfg = defaultConfig()
	}

	// 3. Apply defaults
	cfg.Cards = cfg.Cards.GetDefaults()
	cfg.WB = cfg.WB.GetDefaults()

	// 4. Apply CLI overrides
	if *dbPath != "" {
		cfg.Cards.DbPath = *dbPath
	}

	// 5. Clean database if requested
	if *clean {
		if err := os.Remove(cfg.Cards.DbPath); err != nil && !os.IsNotExist(err) {
			log.Fatalf("❌ Failed to clean database: %v", err)
		}
		fmt.Println("✅ Database cleaned")
	}

	// 6. Print header
	printHeader(cfg, *mockMode, *resume)

	// 7. Handle Ctrl+C
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n⚠️  Прервано")
		cancel()
	}()

	// 8. Open database
	repo, err := sqlite.NewSQLiteSalesRepository(cfg.Cards.DbPath)
	if err != nil {
		log.Fatalf("❌ Failed to open database: %v", err)
	}
	defer repo.Close()

	// 9. Create client (real or mock)
	rl := cfg.Cards.RateLimits

	var client CardsClient
	if *mockMode {
		mockClient := NewMockCardsClient()
		PopulateMockCards(mockClient, 250) // 250 cards = 3 pages with limit 100
		client = mockClient
	} else {
		apiKey := getAPIKey(cfg)
		if apiKey == "" {
			log.Fatal("❌ No API key. Set WB_API_KEY")
		}
		wbClient := wb.New(apiKey)
		wbClient.SetRateLimit("get_cards_list", rl.CardsList, rl.CardsListBurst, rl.CardsListApi, rl.CardsListApiBurst)
		wbClient.SetAdaptiveParams(0, cfg.Cards.AdaptiveProbeAfter, cfg.Cards.MaxBackoffSeconds)
		client = wbClient
		fmt.Printf("API Key: %s\n", maskAPIKey(apiKey))
	}

	// 10. Download data
	result, err := DownloadCards(ctx, client, repo, *resume, rl.CardsList, rl.CardsListBurst, *limit)
	if err != nil {
		log.Fatalf("❌ Failed to download cards: %v", err)
	}

	// 11. Summary
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("✅ Download complete!")
	fmt.Printf("  Cards:     %d\n", result.TotalCards)
	fmt.Printf("  Pages:     %d\n", result.Pages)
	fmt.Printf("  Requests:  %d\n", result.Requests)
	fmt.Printf("  Duration:  %s\n", result.Duration.Round(time.Second))
	fmt.Printf("  Database:  %s\n", cfg.Cards.DbPath)

	count, _ := repo.CountCards(ctx)
	fmt.Printf("  DB total:  %d\n", count)
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func defaultConfig() *Config {
	return &Config{}
}

// getAPIKey retrieves API key with priority: configured env var > standard env vars > config value.
// Pattern B from dev_downloader_development.md.
func getAPIKey(cfg *Config) string {
	envVar := cfg.Cards.APIKeyEnv
	if envVar != "" {
		if key := os.Getenv(envVar); key != "" {
			return key
		}
	}
	if key := os.Getenv("WB_API_KEY"); key != "" {
		return key
	}
	return cfg.WB.APIKey
}

func printHelp() {
	fmt.Print(`WB Content Cards Downloader — загрузка карточек товаров

Usage:
  go run . [options]

Options:
  --config PATH     Путь к конфигу (default: config.yaml)
  --db PATH         Путь к базе (overrides config)
  --resume          Resume from last cursor
  --limit N         Max cards to download (0 = unlimited)
  --clean           Clean database before download
  --mock            Mock mode (no API calls)
  --help            Справка

Examples:
  # Download all cards (cursor-based pagination)
  WB_API_KEY=xxx go run .

  # Download with limit
  WB_API_KEY=xxx go run . --limit 1000

  # Resume after interruption
  WB_API_KEY=xxx go run . --resume

  # Clean and restart
  WB_API_KEY=xxx go run . --clean

  # Mock mode (testing)
  go run . --mock

`)
}

func printHeader(cfg *Config, mock, resume bool) {
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("WB Content Cards Downloader")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Database:  %s\n", cfg.Cards.DbPath)
	if resume {
		fmt.Println("Mode:      Resume")
	}
	if mock {
		fmt.Println("Mode:      Mock")
	}
	fmt.Println(strings.Repeat("=", 60))
}
