package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const version = "2.0.0"

var apiBase = envOr("WH_API_BASE", "https://wallhaven.cc/api/v1/")

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

type Job struct {
	ID          string    `json:"id"`
	PresetID    string    `json:"preset_id"`
	Name        string    `json:"name"`
	Icon        string    `json:"icon"`
	Trigger     string    `json:"trigger"` // manual | all | schedule | rerun
	Status      string    `json:"status"`  // queued | running | done | failed | stopped | interrupted
	Params      Preset    `json:"params"`
	Location    string    `json:"location"`
	QueuedAt    time.Time `json:"queued_at"`
	StartedAt   time.Time `json:"started_at"`
	EndedAt     time.Time `json:"ended_at"`
	Page        int       `json:"page"`
	Pages       int       `json:"pages"`
	Total       int       `json:"total"`
	Target      int       `json:"target"`
	Scanned     int       `json:"scanned"`
	Downloaded  int       `json:"downloaded"`
	Skipped     int       `json:"skipped"`
	Failed      int       `json:"failed"`
	Bytes       int64     `json:"bytes"`
	CurrentFile string    `json:"current_file"`
	Error       string    `json:"error,omitempty"`
}

type StateMsg struct {
	Current *Job   `json:"current"`
	Queue   []Job  `json:"queue"`
	Paused  bool   `json:"paused"`
	HistRev int    `json:"hist_rev"`
	Now     int64  `json:"now"`
	Version string `json:"version"`
}

type App struct {
	mu      sync.Mutex
	dir     string
	cfg     Config
	history []*Job
	histRev int
	queue   []*Job
	current *Job
	cancel  context.CancelFunc
	paused  bool
	dirty   bool
	seq     int
	hub     *Hub
	wake    chan struct{}

	apiMu   sync.Mutex
	lastAPI time.Time

	setsMu sync.Mutex
	sets   map[string]*dlSet

	statsMu   sync.Mutex
	statsAt   time.Time
	statsData *Stats

	http *http.Client
}

func NewApp(dir string) *App {
	a := &App{dir: dir, hub: NewHub(), wake: make(chan struct{}, 1), sets: map[string]*dlSet{},
		http: &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ResponseHeaderTimeout: 45 * time.Second, MaxIdleConnsPerHost: 8}}}
	_ = os.MkdirAll(dir, 0o755)
	a.loadConfig()
	a.loadHistory()
	return a
}

func (a *App) cfgSnap() Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

func (a *App) upd(f func()) {
	a.mu.Lock()
	f()
	a.dirty = true
	a.mu.Unlock()
}

func (a *App) stateMsg() StateMsg {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := StateMsg{Paused: a.paused, HistRev: a.histRev, Now: time.Now().UnixMilli(), Version: version, Queue: []Job{}}
	if a.current != nil {
		c := *a.current
		s.Current = &c
	}
	for _, j := range a.queue {
		s.Queue = append(s.Queue, *j)
	}
	return s
}

func (a *App) pushState() { a.hub.Publish("state", a.stateMsg()) }

func (a *App) broadcaster() {
	t := time.NewTicker(250 * time.Millisecond)
	for range t.C {
		a.mu.Lock()
		d := a.dirty
		a.dirty = false
		a.mu.Unlock()
		if d {
			a.pushState()
		}
	}
}

func (a *App) signal() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

// ── queue management ────────────────────────────────────────────────

func (a *App) enqueue(p Preset, trigger string) (*Job, error) {
	normalize(&p)
	a.mu.Lock()
	defer a.mu.Unlock()
	if p.ID != "" && p.ID != "custom" {
		if a.current != nil && a.current.PresetID == p.ID {
			return nil, fmt.Errorf("%q is already running", p.Name)
		}
		for _, q := range a.queue {
			if q.PresetID == p.ID {
				return nil, fmt.Errorf("%q is already queued", p.Name)
			}
		}
	}
	a.seq++
	j := &Job{ID: fmt.Sprintf("%d-%d", time.Now().Unix(), a.seq), PresetID: p.ID, Name: p.Name, Icon: p.Icon,
		Trigger: trigger, Status: "queued", Params: p, QueuedAt: time.Now()}
	a.queue = append(a.queue, j)
	a.dirty = true
	a.hub.Log(j.ID, "info", "＋ Queued: %s (%s)", p.Name, trigger)
	a.signal()
	return j, nil
}

func (a *App) findPreset(id string) (Preset, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range a.cfg.Presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

func (a *App) runAll(trigger string) int {
	cfg := a.cfgSnap()
	n := 0
	for _, p := range cfg.Presets {
		if p.InAll {
			if _, err := a.enqueue(p, trigger); err == nil {
				n++
			}
		}
	}
	return n
}

func (a *App) worker() {
	for {
		a.mu.Lock()
		var j *Job
		if !a.paused && a.current == nil && len(a.queue) > 0 {
			j = a.queue[0]
			a.queue = a.queue[1:]
			a.current = j
		}
		a.mu.Unlock()
		if j == nil {
			select {
			case <-a.wake:
			case <-time.After(2 * time.Second):
			}
			continue
		}
		a.runJob(j)
	}
}

func (a *App) runJob(j *Job) {
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	j.Status, j.StartedAt, a.cancel = "running", time.Now(), cancel
	a.mu.Unlock()
	a.pushState()
	a.hub.Log(j.ID, "head", "▶ Starting: %s", j.Name)

	err := a.execute(ctx, j)
	stopped := ctx.Err() != nil
	cancel()

	a.mu.Lock()
	j.EndedAt = time.Now()
	j.CurrentFile = ""
	switch {
	case stopped:
		j.Status = "stopped"
	case err != nil:
		j.Status, j.Error = "failed", err.Error()
	default:
		j.Status = "done"
	}
	a.current, a.cancel = nil, nil
	a.history = append(a.history, j)
	if len(a.history) > 300 {
		a.history = a.history[len(a.history)-300:]
	}
	a.histRev++
	a.mu.Unlock()
	a.statsMu.Lock()
	a.statsData = nil
	a.statsMu.Unlock()
	a.saveHistory()

	switch j.Status {
	case "done":
		a.hub.Log(j.ID, "ok", "✔ Finished %s — %d new, %d skipped, %d failed, %s", j.Name, j.Downloaded, j.Skipped, j.Failed, fmtBytes(j.Bytes))
	case "stopped":
		a.hub.Log(j.ID, "warn", "■ Stopped %s — %d new, %d skipped", j.Name, j.Downloaded, j.Skipped)
	default:
		a.hub.Log(j.ID, "err", "✖ Failed %s: %s", j.Name, j.Error)
	}
	a.pushState()
	a.signal()
}

func (a *App) stopCurrent() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
		return true
	}
	return false
}

func fmtBytes(n int64) string {
	f := float64(n)
	u := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	for f >= 1024 && i < len(u)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", f, u[i])
}

// ── Wallhaven API ───────────────────────────────────────────────────

type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	if fl, err := strconv.ParseFloat(s, 64); err == nil {
		*f = flexInt(int(fl))
	}
	return nil
}

type item struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	Resolution string `json:"resolution"`
	Purity     string `json:"purity"`
	Category   string `json:"category"`
	Thumbs     struct {
		Large    string `json:"large"`
		Original string `json:"original"`
		Small    string `json:"small"`
	} `json:"thumbs"`
}

type searchResp struct {
	Data []item `json:"data"`
	Meta struct {
		CurrentPage flexInt `json:"current_page"`
		LastPage    flexInt `json:"last_page"`
		PerPage     flexInt `json:"per_page"`
		Total       flexInt `json:"total"`
		Seed        *string `json:"seed"`
	} `json:"meta"`
}

func searchPath(p Preset, page int, seed string) string {
	v := url.Values{}
	v.Set("page", strconv.Itoa(page))
	set := func(k, val string) {
		if val != "" {
			v.Set(k, val)
		}
	}
	set("categories", p.Categories)
	set("purity", p.Purity)
	set("sorting", p.Sorting)
	set("order", p.Order)
	if p.AtLeast != "" {
		v.Set("atleast", p.AtLeast)
	} else {
		set("resolutions", p.Resolutions)
	}
	set("ratios", p.Ratios)
	set("colors", p.Colors)
	if p.Sorting == "toplist" {
		set("topRange", p.TopRange)
	}
	if p.Sorting == "random" {
		set("seed", seed)
	}
	switch p.Type {
	case "search":
		set("q", p.Query)
	case "useruploads":
		set("q", "@"+p.User)
	}
	return "search?" + v.Encode()
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (a *App) apiGet(ctx context.Context, rel string) ([]byte, error) {
	cfg := a.cfgSnap()
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		a.apiMu.Lock()
		wait := time.Until(a.lastAPI)
		if wait < 0 {
			wait = 0
		}
		a.lastAPI = time.Now().Add(wait + time.Duration(cfg.APIDelayMs)*time.Millisecond)
		a.apiMu.Unlock()
		if wait > 0 && !sleepCtx(ctx, wait) {
			return nil, ctx.Err()
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", apiBase+rel, nil)
		req.Header.Set("User-Agent", "wallhaven-control/"+version)
		if cfg.APIKey != "" {
			req.Header.Set("X-API-Key", cfg.APIKey)
		}
		resp, err := a.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			a.hub.Log("", "warn", "API request failed (%v) — retrying", err)
			if !sleepCtx(ctx, 3*time.Second) {
				return nil, ctx.Err()
			}
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		switch resp.StatusCode {
		case 200:
			return body, nil
		case 429:
			a.hub.Log("", "warn", "⏳ Rate limited by Wallhaven — sleeping %ds...", cfg.CooldownSec)
			lastErr = errors.New("rate limited (429)")
			if !sleepCtx(ctx, time.Duration(cfg.CooldownSec)*time.Second) {
				return nil, ctx.Err()
			}
		case 401:
			return nil, errors.New("401 Unauthorized — check your API key in Settings")
		default:
			return nil, fmt.Errorf("HTTP %d from Wallhaven API", resp.StatusCode)
		}
	}
	return nil, fmt.Errorf("giving up after retries: %v", lastErr)
}

func (a *App) downloadFile(ctx context.Context, src, dest string) (int64, error) {
	cfg := a.cfgSnap()
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		n, retry, err := a.downloadOnce(ctx, src, dest, cfg)
		if err == nil {
			return n, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if !retry {
			break
		}
	}
	return 0, lastErr
}

func (a *App) downloadOnce(ctx context.Context, src, dest string, cfg Config) (int64, bool, error) {
	rctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(rctx, "GET", src, nil)
	req.Header.Set("User-Agent", "wallhaven-control/"+version)
	resp, err := a.http.Do(req)
	if err != nil {
		sleepCtx(ctx, 2*time.Second)
		return 0, true, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 {
		a.hub.Log("", "warn", "⏳ Rate limited — sleeping %ds...", cfg.CooldownSec)
		sleepCtx(ctx, time.Duration(cfg.CooldownSec)*time.Second)
		return 0, true, errors.New("HTTP 429")
	}
	if resp.StatusCode != 200 {
		return 0, false, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return 0, false, err
	}
	n, err := io.Copy(f, resp.Body)
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return 0, true, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return 0, false, err
	}
	return n, false, nil
}

// ── downloaded.txt compatible tracking ──────────────────────────────

type dlSet struct {
	mu   sync.Mutex
	m    map[string]struct{}
	path string
}

func (a *App) getSet(loc string) *dlSet {
	a.setsMu.Lock()
	defer a.setsMu.Unlock()
	if s, ok := a.sets[loc]; ok {
		return s
	}
	s := &dlSet{m: map[string]struct{}{}, path: filepath.Join(loc, "downloaded.txt")}
	if f, err := os.Open(s.path); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if t := strings.TrimSpace(sc.Text()); t != "" {
				s.m[t] = struct{}{}
			}
		}
		f.Close()
	}
	a.sets[loc] = s
	return s
}

func (s *dlSet) has(n string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.m[n]
	return ok
}

func (s *dlSet) size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}

func (s *dlSet) add(n string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[n]; ok {
		return
	}
	s.m[n] = struct{}{}
	if f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintln(f, n)
		f.Close()
	}
}

// ── the actual download run (port of runDownload / downloadWallpapers) ─

func (a *App) locationFor(cfg Config, p Preset) string {
	loc := p.Location
	if !filepath.IsAbs(loc) {
		loc = filepath.Join(cfg.BaseDir, loc)
	}
	return loc
}

func sanitize(s string) string {
	return strings.NewReplacer(" ", "_", "+", "_", "/", "_", "\\", "_", ":", "_").Replace(s)
}

func (a *App) execute(ctx context.Context, j *Job) error {
	p := j.Params
	cfg := a.cfgSnap()
	loc := a.locationFor(cfg, p)
	if p.Type == "search" && p.Subfolder && p.Query != "" {
		loc = filepath.Join(loc, sanitize(p.Query))
	}
	if err := os.MkdirAll(loc, 0o755); err != nil {
		return fmt.Errorf("cannot create %s: %w", loc, err)
	}
	a.upd(func() { j.Location = loc })
	a.hub.Log(j.ID, "info", "📁 Destination: %s", loc)
	a.hub.Log(j.ID, "info", "🔍 type=%s query=%q categories=%s purity=%s count=%d", p.Type, p.Query, p.Categories, p.Purity, p.Count)
	if cfg.APIKey == "" && p.Purity[2] == '1' {
		a.hub.Log(j.ID, "warn", "No API key set — NSFW results will be empty")
	}
	set := a.getSet(loc)

	pathFor := func(page int, seed string) string { return searchPath(p, page, seed) }
	collectionCount := 0
	if p.Type == "collections" {
		if p.User == "" {
			return errors.New("collections need a User")
		}
		b, err := a.apiGet(ctx, "collections/"+url.PathEscape(p.User))
		if err != nil {
			return err
		}
		var cl struct {
			Data []struct {
				ID    flexInt `json:"id"`
				Label string  `json:"label"`
				Count flexInt `json:"count"`
			} `json:"data"`
		}
		if err := json.Unmarshal(b, &cl); err != nil {
			return err
		}
		id := -1
		for _, c := range cl.Data {
			if c.Label == p.Collection {
				id, collectionCount = int(c.ID), int(c.Count)
			}
		}
		if id < 0 {
			return fmt.Errorf("collection %q does not exist for user %s", p.Collection, p.User)
		}
		pathFor = func(page int, _ string) string {
			return fmt.Sprintf("collections/%s/%d?page=%d", url.PathEscape(p.User), id, page)
		}
	}

	seed := ""
	scanned, newDL := 0, 0
	for page := p.StartPage; ; page++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		a.hub.Log(j.ID, "head", "📄 Fetching page %d...", page)
		b, err := a.apiGet(ctx, pathFor(page, seed))
		if err != nil {
			return err
		}
		var r searchResp
		if err := json.Unmarshal(b, &r); err != nil {
			return fmt.Errorf("bad API response: %w", err)
		}
		if r.Meta.Seed != nil {
			seed = *r.Meta.Seed
		}
		per, last, total := int(r.Meta.PerPage), int(r.Meta.LastPage), int(r.Meta.Total)
		if collectionCount > 0 && total == 0 {
			total = collectionCount
		}
		if per <= 0 {
			per = len(r.Data)
		}
		a.upd(func() {
			j.Page, j.Total = page, total
			target := p.Count
			if !p.CountNew && total > 0 && total < target {
				target = total
			}
			j.Target = target
			pages := last
			if !p.CountNew && per > 0 {
				if need := p.StartPage + int(math.Ceil(float64(p.Count)/float64(per))) - 1; need < pages || pages == 0 {
					pages = need
				}
			}
			j.Pages = pages
		})
		if page == p.StartPage {
			a.hub.Log(j.ID, "info", "   %d results, %d per page, %d pages", total, per, last)
		}
		items := r.Data
		if len(items) == 0 {
			a.hub.Log(j.ID, "warn", "No results on this page — stopping")
			break
		}
		if !p.CountNew && scanned+len(items) > p.Count {
			items = items[:p.Count-scanned]
		}
		var todo []item
		for _, it := range items {
			name := path.Base(it.Path)
			if it.Path == "" {
				a.upd(func() { j.Failed++; j.Scanned++ })
				continue
			}
			if set.has(name) {
				a.upd(func() { j.Skipped++; j.Scanned++ })
				a.hub.Log(j.ID, "skip", "⏭ Already downloaded: %s", name)
				continue
			}
			todo = append(todo, it)
		}
		if p.CountNew && newDL+len(todo) > p.Count {
			todo = todo[:p.Count-newDL]
		}
		a.hub.Log(j.ID, "info", "📥 Downloading %d wallpapers from page %d...", len(todo), page)
		sem := make(chan struct{}, cfg.Concurrency)
		var wg sync.WaitGroup
		for _, it := range todo {
			if ctx.Err() != nil {
				break
			}
			sem <- struct{}{}
			wg.Add(1)
			go func(it item) {
				defer func() { <-sem; wg.Done() }()
				name := path.Base(it.Path)
				a.upd(func() { j.CurrentFile = name })
				n, err := a.downloadFile(ctx, it.Path, filepath.Join(loc, name))
				if err != nil {
					if ctx.Err() == nil {
						a.upd(func() { j.Failed++; j.Scanned++ })
						a.hub.Log(j.ID, "err", "⚠ Failed: %s (%v)", name, err)
					}
					return
				}
				set.add(name)
				a.upd(func() { j.Downloaded++; j.Scanned++; j.Bytes += n })
				a.hub.Log(j.ID, "ok", "⬇ Downloaded: %s  %s  %s", name, it.Resolution, fmtBytes(n))
			}(it)
		}
		wg.Wait()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		scanned += len(items)
		newDL += len(todo)
		a.hub.Log(j.ID, "ok", "   ✅ Page %d done", page)
		if last > 0 && page >= last {
			break
		}
		if p.CountNew && newDL >= p.Count {
			break
		}
		if !p.CountNew && scanned >= p.Count {
			break
		}
		if p.Type == "collections" && collectionCount > 0 && scanned >= collectionCount {
			break
		}
	}
	return nil
}

// ── scheduler ───────────────────────────────────────────────────────

func (a *App) scheduler() {
	t := time.NewTicker(30 * time.Second)
	for range t.C {
		a.mu.Lock()
		var due []string
		changed := false
		for i := range a.cfg.Schedules {
			s := &a.cfg.Schedules[i]
			if !s.Enabled || s.IntervalMin <= 0 {
				continue
			}
			if s.LastRun.IsZero() {
				s.LastRun = time.Now()
				changed = true
				continue
			}
			if time.Since(s.LastRun) >= time.Duration(s.IntervalMin)*time.Minute {
				s.LastRun = time.Now()
				due = append(due, s.Preset)
				changed = true
			}
		}
		if changed {
			a.saveConfigLocked()
		}
		a.mu.Unlock()
		for _, id := range due {
			a.hub.Log("", "info", "⏰ Schedule fired: %s", id)
			if id == "all" {
				a.runAll("schedule")
			} else if p, ok := a.findPreset(id); ok {
				a.enqueue(p, "schedule")
			}
		}
	}
}
