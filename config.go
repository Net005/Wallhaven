package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Preset is one saved search/download profile (the equivalent of a _preset_xxx() function in the shell script).
type Preset struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	Location    string `json:"location"`
	Type        string `json:"type"` // search | standard | collections | useruploads
	Query       string `json:"query"`
	Categories  string `json:"categories"` // general/anime/people, e.g. 101
	Purity      string `json:"purity"`     // sfw/sketchy/nsfw, e.g. 110
	Count       int    `json:"count"`
	StartPage   int    `json:"start_page"`
	AtLeast     string `json:"atleast"`
	Resolutions string `json:"resolutions"`
	Ratios      string `json:"ratios"`
	Sorting     string `json:"sorting"`
	Order       string `json:"order"`
	TopRange    string `json:"top_range"`
	Colors      string `json:"colors"`
	User        string `json:"user"`
	Collection  string `json:"collection"`
	Subfolder   bool   `json:"subfolder"`
	CountNew    bool   `json:"count_new"` // Count = number of NEW wallpapers instead of results scanned
	InAll       bool   `json:"in_all"`    // included in "Run all"
}

type Schedule struct {
	ID          string    `json:"id"`
	Preset      string    `json:"preset"` // preset id or "all"
	IntervalMin int       `json:"interval_min"`
	Enabled     bool      `json:"enabled"`
	LastRun     time.Time `json:"last_run"`
}

type Config struct {
	APIKey      string     `json:"api_key"`
	BaseDir     string     `json:"base_dir"`
	Concurrency int        `json:"concurrency"`
	APIDelayMs  int        `json:"api_delay_ms"`
	CooldownSec int        `json:"cooldown_sec"`
	AuthUser    string     `json:"auth_user"`
	AuthPass    string     `json:"auth_pass"`
	Presets     []Preset   `json:"presets"`
	Schedules   []Schedule `json:"schedules"`
}

func basePreset() Preset {
	return Preset{
		Type: "search", Categories: "100", Purity: "110", Count: 640, StartPage: 1,
		AtLeast: "1920x1080", Ratios: "16x9,16x10,21x9", Sorting: "date_added", Order: "desc", InAll: true,
	}
}

func mk(id, name, icon, loc, cat, pur, q string) Preset {
	p := basePreset()
	p.ID, p.Name, p.Icon, p.Location, p.Categories, p.Purity, p.Query = id, name, icon, loc, cat, pur, q
	return p
}

// Seeded from scrape-all.sh
func seedPresets() []Preset {
	return []Preset{
		mk("anime-sfw", "Anime wallpapers (SFW)", "🌸", "Anime/SFW", "010", "100", "id:1"),
		mk("anime-sketchy", "Anime wallpapers (Sketchy)", "🎭", "Anime/Sketchy", "010", "010", "id:1"),
		mk("anime-nsfw", "Anime wallpapers (NSFW)", "🔥", "Anime/NSFW", "010", "001", "id:1"),
		mk("asian-women", "Asian women wallpapers", "👩", "Asian/Women", "001", "011", "id:449"),
		mk("digitalart", "Digital Art wallpapers", "🎨", "DigitalArt", "100", "110", "id:479"),
		mk("games", "Games wallpapers", "🎮", "Games", "101", "110", "id:55"),
		mk("japanese-women", "Japanese women wallpapers", "🎌", "Asian/Women", "001", "011", "id:31296"),
		mk("nature", "Nature wallpapers", "🌿", "Nature", "100", "110", "id:37"),
		mk("nebula", "Nebula wallpapers", "🌌", "Nebula", "101", "110", "id:90"),
		mk("pornstar", "Pornstar wallpapers", "💋", "Pornstars", "001", "011", "id:355"),
		mk("space", "Space wallpapers", "🚀", "Space", "101", "110", "id:32"),
		mk("women", "Women wallpapers", "💃", "Women", "001", "011", "id:222"),
	}
}

func defaultConfig() Config {
	c := Config{
		BaseDir: "/wallpapers/Wallhaven", Concurrency: 2, APIDelayMs: 1400, CooldownSec: 30,
		Presets: seedPresets(),
	}
	if v := os.Getenv("WH_BASE_DIR"); v != "" {
		c.BaseDir = v
	}
	if v := os.Getenv("WH_API_KEY"); v != "" {
		c.APIKey = v
	}
	return c
}

var (
	reBits = regexp.MustCompile(`^[01]{3}$`)
	reSlug = regexp.MustCompile(`[^a-z0-9]+`)
)

func slug(s string) string {
	s = strings.Trim(reSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if s == "" {
		s = "preset"
	}
	return s
}

func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// normalize fills defaults and validates a preset.
func normalize(p *Preset) {
	d := basePreset()
	p.Name = strings.TrimSpace(p.Name)
	p.Query = strings.TrimSpace(strings.ReplaceAll(p.Query, "'", ""))
	p.Location = strings.TrimSpace(p.Location)
	p.Colors = strings.TrimPrefix(strings.TrimSpace(p.Colors), "#")
	p.User = strings.TrimSpace(p.User)
	p.Collection = strings.TrimSpace(p.Collection)
	if !oneOf(p.Type, "search", "standard", "collections", "useruploads") {
		p.Type = d.Type
	}
	if !reBits.MatchString(p.Categories) {
		p.Categories = d.Categories
	}
	if !reBits.MatchString(p.Purity) {
		p.Purity = d.Purity
	}
	if p.Count <= 0 {
		p.Count = d.Count
	}
	if p.StartPage <= 0 {
		p.StartPage = 1
	}
	if !oneOf(p.Sorting, "date_added", "relevance", "random", "views", "favorites", "toplist", "hot") {
		p.Sorting = d.Sorting
	}
	if !oneOf(p.Order, "asc", "desc") {
		p.Order = "desc"
	}
	if p.Name == "" {
		p.Name = "Untitled preset"
	}
	if p.Icon == "" {
		p.Icon = "🖼️"
	}
	if p.Location == "" {
		p.Location = slug(p.Name)
	}
}

func (a *App) configPath() string  { return filepath.Join(a.dir, "config.json") }
func (a *App) historyPath() string { return filepath.Join(a.dir, "history.json") }

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (a *App) loadConfig() {
	a.cfg = defaultConfig()
	if b, err := os.ReadFile(a.configPath()); err == nil {
		var c Config
		if json.Unmarshal(b, &c) == nil {
			a.cfg = c
			if a.cfg.Concurrency < 1 {
				a.cfg.Concurrency = 2
			}
			if a.cfg.APIDelayMs < 0 {
				a.cfg.APIDelayMs = 1400
			}
			if a.cfg.CooldownSec < 1 {
				a.cfg.CooldownSec = 30
			}
			if a.cfg.BaseDir == "" {
				a.cfg.BaseDir = "/wallpapers/Wallhaven"
			}
		}
	} else {
		a.saveConfigLocked()
	}
	if v := os.Getenv("WH_USER"); v != "" {
		a.cfg.AuthUser = v
	}
	if v := os.Getenv("WH_PASS"); v != "" {
		a.cfg.AuthPass = v
	}
}

func (a *App) saveConfigLocked() {
	b, _ := json.MarshalIndent(a.cfg, "", "  ")
	_ = os.MkdirAll(a.dir, 0o755)
	_ = writeFileAtomic(a.configPath(), b, 0o600)
}

func (a *App) loadHistory() {
	b, err := os.ReadFile(a.historyPath())
	if err != nil {
		return
	}
	var h []*Job
	if json.Unmarshal(b, &h) != nil {
		return
	}
	for _, j := range h {
		if j.Status == "running" || j.Status == "queued" {
			j.Status = "interrupted"
		}
		a.seq++
	}
	a.history = h
}

func (a *App) saveHistory() {
	a.mu.Lock()
	b, _ := json.Marshal(a.history)
	a.mu.Unlock()
	_ = writeFileAtomic(a.historyPath(), b, 0o644)
}
