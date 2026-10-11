// Package apispecs refreshes OpenAPI/Swagger spec files from vendor portals
// (dev.wildberries.ru, docs.ozon.ru) into the local docs/ folders.
//
// Two channels:
//   - HTTP: plain GET with a browser User-Agent — fast, works while the portal
//     is not gate-keeping this IP;
//   - CDP: a real Chromium browser driven over the DevTools Protocol — passes
//     JS challenges, and can capture specs that a docs site loads itself
//     (Ozon publishes no direct spec URL).
//
// The package is deliberately read-only: no git operations, writes only to the
// configured output paths, old file stays untouched on failure.
package apispecs

// Config — YAML configuration of the spec downloader.
type Config struct {
	HTTP    HTTPConfig `yaml:"http"`
	CDP     CDPConfig  `yaml:"cdp"`
	Targets []Target   `yaml:"targets"`
}

// HTTPConfig — settings of the plain-HTTP channel.
type HTTPConfig struct {
	UserAgent      string `yaml:"user_agent"`
	TimeoutSeconds int    `yaml:"timeout_seconds"` // per request
	PauseMs        int    `yaml:"pause_ms"`        // between requests (politeness)
	Retries        int    `yaml:"retries"`         // attempts for transient failures
}

// CDPConfig — settings of the browser channel.
type CDPConfig struct {
	BrowserPath    string `yaml:"browser_path"`    // Edge/Chrome binary; empty = auto-detect
	ProfileDir     string `yaml:"profile_dir"`     // persistent user-data-dir (fresh profile, NOT a copy of the daily one)
	Headless       bool   `yaml:"headless"`        // headed by default: least bot-like
	TimeoutSeconds int    `yaml:"timeout_seconds"` // page settle / capture budget
}

// Target — one vendor section of the config.
//
// Files — direct download list (WB: base URL + file name per spec).
// Capture — CDP network capture for portals with no direct spec URL (Ozon):
// open the docs page and grab the OpenAPI JSON that the page itself loads.
type Target struct {
	Name    string       `yaml:"name"`
	Files   []FileSpec   `yaml:"files"`
	Capture *CaptureSpec `yaml:"capture"`
}

// FileSpec — one remotely fetched file.
type FileSpec struct {
	URL  string `yaml:"url"`
	Path string `yaml:"path"` // output, relative to the repo root
}

// CaptureSpec — one CDP capture → one or more identical local copies.
type CaptureSpec struct {
	PageURL  string   `yaml:"page_url"`
	Contains []string `yaml:"contains"` // candidate URL substrings (filtering + diagnostics)
	MinSize  int64    `yaml:"min_size"` // plausible spec size floor, bytes
	Outputs  []string `yaml:"outputs"`  // relative to the repo root
}

// GetDefaults fills unset values with sensible production defaults.
func (c Config) GetDefaults() Config {
	if c.HTTP.UserAgent == "" {
		c.HTTP.UserAgent = DefaultUserAgent
	}
	if c.HTTP.TimeoutSeconds == 0 {
		c.HTTP.TimeoutSeconds = 30
	}
	if c.HTTP.PauseMs == 0 {
		c.HTTP.PauseMs = 1000
	}
	if c.HTTP.Retries == 0 {
		c.HTTP.Retries = 3
	}
	if c.CDP.ProfileDir == "" {
		c.CDP.ProfileDir = "~/.cache/poncho-cdp-profile"
	}
	if c.CDP.TimeoutSeconds == 0 {
		c.CDP.TimeoutSeconds = 90
	}
	return c
}
