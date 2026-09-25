package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func jerr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func readJSON(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	return json.NewDecoder(r.Body).Decode(v)
}

var imgExt = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true}

func (a *App) routes() http.Handler {
	m := http.NewServeMux()

	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	m.HandleFunc("GET /api/events", a.handleEvents)
	m.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, a.stateMsg()) })
	m.HandleFunc("GET /api/history", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		out := make([]*Job, 0, len(a.history))
		for i := len(a.history) - 1; i >= 0; i-- {
			out = append(out, a.history[i])
		}
		a.mu.Unlock()
		writeJSON(w, 200, out)
	})
	m.HandleFunc("POST /api/history/clear", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.history = nil
		a.histRev++
		a.mu.Unlock()
		a.saveHistory()
		a.pushState()
		writeJSON(w, 200, map[string]bool{"ok": true})
	})

	// config / settings
	m.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, a.publicConfig()) })
	m.HandleFunc("PUT /api/settings", a.handleSettings)
	m.HandleFunc("POST /api/test-key", a.handleTestKey)

	// presets
	m.HandleFunc("POST /api/presets", func(w http.ResponseWriter, r *http.Request) {
		var p Preset
		if err := readJSON(r, &p); err != nil {
			jerr(w, 400, err)
			return
		}
		normalize(&p)
		a.mu.Lock()
		base := slug(p.Name)
		id := base
		for n := 2; a.hasPresetLocked(id); n++ {
			id = fmt.Sprintf("%s-%d", base, n)
		}
		p.ID = id
		a.cfg.Presets = append(a.cfg.Presets, p)
		a.saveConfigLocked()
		a.mu.Unlock()
		writeJSON(w, 200, p)
	})
	m.HandleFunc("PUT /api/presets/{id}", func(w http.ResponseWriter, r *http.Request) {
		var p Preset
		if err := readJSON(r, &p); err != nil {
			jerr(w, 400, err)
			return
		}
		normalize(&p)
		p.ID = r.PathValue("id")
		a.mu.Lock()
		defer a.mu.Unlock()
		for i := range a.cfg.Presets {
			if a.cfg.Presets[i].ID == p.ID {
				a.cfg.Presets[i] = p
				a.saveConfigLocked()
				writeJSON(w, 200, p)
				return
			}
		}
		jerr(w, 404, fmt.Errorf("unknown preset"))
	})
	m.HandleFunc("DELETE /api/presets/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		a.mu.Lock()
		defer a.mu.Unlock()
		for i := range a.cfg.Presets {
			if a.cfg.Presets[i].ID == id {
				a.cfg.Presets = append(a.cfg.Presets[:i], a.cfg.Presets[i+1:]...)
				a.saveConfigLocked()
				writeJSON(w, 200, map[string]bool{"ok": true})
				return
			}
		}
		jerr(w, 404, fmt.Errorf("unknown preset"))
	})
	m.HandleFunc("POST /api/presets/move", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			ID  string `json:"id"`
			Dir int    `json:"dir"`
		}
		if readJSON(r, &b) != nil {
			jerr(w, 400, fmt.Errorf("bad request"))
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		ps := a.cfg.Presets
		for i := range ps {
			j := i + b.Dir
			if ps[i].ID == b.ID && j >= 0 && j < len(ps) {
				ps[i], ps[j] = ps[j], ps[i]
				a.saveConfigLocked()
				break
			}
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("PUT /api/schedules", func(w http.ResponseWriter, r *http.Request) {
		var s []Schedule
		if err := readJSON(r, &s); err != nil {
			jerr(w, 400, err)
			return
		}
		a.mu.Lock()
		old := map[string]Schedule{}
		for _, o := range a.cfg.Schedules {
			old[o.ID] = o
		}
		for i := range s {
			if s[i].ID == "" {
				s[i].ID = strconv.FormatInt(time.Now().UnixNano(), 36)
			}
			if o, ok := old[s[i].ID]; ok && s[i].LastRun.IsZero() {
				s[i].LastRun = o.LastRun
			}
			if s[i].LastRun.IsZero() {
				s[i].LastRun = time.Now()
			}
		}
		a.cfg.Schedules = s
		a.saveConfigLocked()
		a.mu.Unlock()
		writeJSON(w, 200, s)
	})

	// runs / queue
	m.HandleFunc("POST /api/run", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			PresetID  string `json:"preset_id"`
			Count     *int   `json:"count"`
			StartPage *int   `json:"start_page"`
			CountNew  *bool  `json:"count_new"`
		}
		if err := readJSON(r, &b); err != nil {
			jerr(w, 400, err)
			return
		}
		p, ok := a.findPreset(b.PresetID)
		if !ok {
			jerr(w, 404, fmt.Errorf("unknown preset"))
			return
		}
		if b.Count != nil {
			p.Count = *b.Count
		}
		if b.StartPage != nil {
			p.StartPage = *b.StartPage
		}
		if b.CountNew != nil {
			p.CountNew = *b.CountNew
		}
		j, err := a.enqueue(p, "manual")
		if err != nil {
			jerr(w, 409, err)
			return
		}
		writeJSON(w, 200, j)
	})
	m.HandleFunc("POST /api/run-custom", func(w http.ResponseWriter, r *http.Request) {
		var p Preset
		if err := readJSON(r, &p); err != nil {
			jerr(w, 400, err)
			return
		}
		p.ID = "custom"
		if strings.TrimSpace(p.Name) == "" {
			p.Name = "Custom run"
		}
		j, err := a.enqueue(p, "manual")
		if err != nil {
			jerr(w, 409, err)
			return
		}
		writeJSON(w, 200, j)
	})
	m.HandleFunc("POST /api/run-all", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]int{"queued": a.runAll("all")})
	})
	m.HandleFunc("POST /api/jobs/{id}/rerun", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		var p *Preset
		for _, j := range a.history {
			if j.ID == r.PathValue("id") {
				c := j.Params
				c.ID = j.PresetID
				p = &c
			}
		}
		a.mu.Unlock()
		if p == nil {
			jerr(w, 404, fmt.Errorf("job not found"))
			return
		}
		j, err := a.enqueue(*p, "rerun")
		if err != nil {
			jerr(w, 409, err)
			return
		}
		writeJSON(w, 200, j)
	})
	m.HandleFunc("POST /api/queue/pause", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.paused = true
		a.mu.Unlock()
		a.hub.Log("", "warn", "⏸ Queue paused (current job keeps running)")
		a.pushState()
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("POST /api/queue/resume", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.paused = false
		a.mu.Unlock()
		a.hub.Log("", "info", "▶ Queue resumed")
		a.signal()
		a.pushState()
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("POST /api/queue/clear", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		n := len(a.queue)
		a.queue = nil
		a.mu.Unlock()
		a.hub.Log("", "warn", "Cleared %d queued job(s)", n)
		a.pushState()
		writeJSON(w, 200, map[string]int{"cleared": n})
	})
	m.HandleFunc("POST /api/stop", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("all") == "1" {
			a.mu.Lock()
			a.queue = nil
			a.mu.Unlock()
		}
		writeJSON(w, 200, map[string]bool{"stopped": a.stopCurrent()})
	})
	m.HandleFunc("DELETE /api/queue/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		for i, j := range a.queue {
			if j.ID == r.PathValue("id") {
				a.queue = append(a.queue[:i], a.queue[i+1:]...)
				break
			}
		}
		a.mu.Unlock()
		a.pushState()
		writeJSON(w, 200, map[string]bool{"ok": true})
	})

	// logs
	m.HandleFunc("POST /api/logs/clear", func(w http.ResponseWriter, r *http.Request) {
		a.hub.ClearLogs()
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("GET /api/logs/download", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="wallhaven-control.log"`)
		for _, l := range a.hub.Backlog() {
			fmt.Fprintf(w, "%s [%-4s] %s\n", time.UnixMilli(l.T).Format("2006-01-02 15:04:05"), l.Lvl, l.Msg)
		}
	})

	// stats, gallery, files, preview
	m.HandleFunc("GET /api/stats", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, a.stats()) })
	m.HandleFunc("GET /api/gallery", a.handleGallery)
	m.HandleFunc("GET /files/{preset}/{name}", a.handleFile)
	m.HandleFunc("DELETE /api/files/{preset}/{name}", a.handleDeleteFile)
	m.HandleFunc("POST /api/preview", a.handlePreview)

	m.Handle("/", http.FileServer(http.FS(webRoot())))
	return a.auth(m)
}

func (a *App) hasPresetLocked(id string) bool {
	for _, p := range a.cfg.Presets {
		if p.ID == id {
			return true
		}
	}
	return false
}

func (a *App) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := a.cfgSnap()
		if cfg.AuthUser != "" && cfg.AuthPass != "" && r.URL.Path != "/healthz" {
			u, p, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(cfg.AuthUser)) != 1 ||
				subtle.ConstantTimeCompare([]byte(p), []byte(cfg.AuthPass)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="Wallhaven Control"`)
				http.Error(w, "unauthorized", 401)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no streaming", 500)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	ch := a.hub.Sub()
	defer a.hub.Unsub(ch)
	send := func(ev string, data []byte) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev, data)
	}
	bl, _ := json.Marshal(a.hub.Backlog())
	send("backlog", bl)
	st, _ := json.Marshal(a.stateMsg())
	send("state", st)
	fl.Flush()
	hb := time.NewTicker(15 * time.Second)
	defer hb.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case m := <-ch:
			send(m.Event, m.Data)
			// drain what's ready to reduce flush count
		drain:
			for n := 0; n < 200; n++ {
				select {
				case m2 := <-ch:
					send(m2.Event, m2.Data)
				default:
					break drain
				}
			}
			fl.Flush()
		case <-hb.C:
			fmt.Fprint(w, ": hb\n\n")
			fl.Flush()
		}
	}
}

func (a *App) publicConfig() map[string]any {
	c := a.cfgSnap()
	hint := ""
	if len(c.APIKey) > 4 {
		hint = "••••••••" + c.APIKey[len(c.APIKey)-4:]
	} else if c.APIKey != "" {
		hint = "••••"
	}
	return map[string]any{
		"api_key_set": c.APIKey != "", "api_key_hint": hint, "base_dir": c.BaseDir,
		"concurrency": c.Concurrency, "api_delay_ms": c.APIDelayMs, "cooldown_sec": c.CooldownSec,
		"auth_user": c.AuthUser, "auth_enabled": c.AuthUser != "" && c.AuthPass != "",
		"presets": c.Presets, "schedules": c.Schedules, "version": version,
	}
}

func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	var b struct {
		APIKey      *string `json:"api_key"`
		BaseDir     string  `json:"base_dir"`
		Concurrency int     `json:"concurrency"`
		APIDelayMs  int     `json:"api_delay_ms"`
		CooldownSec int     `json:"cooldown_sec"`
		AuthUser    string  `json:"auth_user"`
		AuthPass    *string `json:"auth_pass"`
	}
	if err := readJSON(r, &b); err != nil {
		jerr(w, 400, err)
		return
	}
	a.mu.Lock()
	if b.APIKey != nil {
		k := strings.TrimSpace(*b.APIKey)
		if k == "-" {
			a.cfg.APIKey = ""
		} else if k != "" {
			a.cfg.APIKey = k
		}
	}
	if strings.TrimSpace(b.BaseDir) != "" {
		a.cfg.BaseDir = strings.TrimSpace(b.BaseDir)
	}
	if b.Concurrency >= 1 && b.Concurrency <= 16 {
		a.cfg.Concurrency = b.Concurrency
	}
	if b.APIDelayMs >= 0 {
		a.cfg.APIDelayMs = b.APIDelayMs
	}
	if b.CooldownSec >= 1 {
		a.cfg.CooldownSec = b.CooldownSec
	}
	a.cfg.AuthUser = strings.TrimSpace(b.AuthUser)
	if b.AuthPass != nil && *b.AuthPass != "" {
		a.cfg.AuthPass = *b.AuthPass
	}
	if a.cfg.AuthUser == "" {
		a.cfg.AuthPass = ""
	}
	a.saveConfigLocked()
	a.mu.Unlock()
	writeJSON(w, 200, a.publicConfig())
}

func (a *App) handleTestKey(w http.ResponseWriter, r *http.Request) {
	b, err := a.apiGet(r.Context(), "settings")
	if err != nil {
		jerr(w, 400, err)
		return
	}
	var out any
	json.Unmarshal(b, &out)
	writeJSON(w, 200, out)
}

func (a *App) handlePreview(w http.ResponseWriter, r *http.Request) {
	var p Preset
	if err := readJSON(r, &p); err != nil {
		jerr(w, 400, err)
		return
	}
	normalize(&p)
	if p.Type == "collections" {
		jerr(w, 400, fmt.Errorf("preview is not available for collections"))
		return
	}
	b, err := a.apiGet(r.Context(), searchPath(p, 1, ""))
	if err != nil {
		jerr(w, 400, err)
		return
	}
	var resp searchResp
	if err := json.Unmarshal(b, &resp); err != nil {
		jerr(w, 502, err)
		return
	}
	out := []map[string]string{}
	for i, it := range resp.Data {
		if i >= 12 {
			break
		}
		out = append(out, map[string]string{"id": it.ID, "thumb": it.Thumbs.Small, "res": it.Resolution, "purity": it.Purity, "url": "https://wallhaven.cc/w/" + it.ID})
	}
	writeJSON(w, 200, map[string]any{"total": int(resp.Meta.Total), "per_page": int(resp.Meta.PerPage), "pages": int(resp.Meta.LastPage), "items": out})
}

// ── library stats / gallery ─────────────────────────────────────────

type PresetStat struct {
	ID         string     `json:"id"`
	Location   string     `json:"location"`
	Files      int        `json:"files"`
	Bytes      int64      `json:"bytes"`
	Tracked    int        `json:"tracked"`
	LastRun    *time.Time `json:"last_run"`
	LastStatus string     `json:"last_status"`
}

type Stats struct {
	Presets    []PresetStat `json:"presets"`
	TotalFiles int          `json:"total_files"`
	TotalBytes int64        `json:"total_bytes"`
	DiskFree   uint64       `json:"disk_free"`
	DiskTotal  uint64       `json:"disk_total"`
	RunsTotal  int          `json:"runs_total"`
	Runs24h    int          `json:"runs_24h"`
	New24h     int          `json:"new_24h"`
	Bytes24h   int64        `json:"bytes_24h"`
}

func scanDir(loc string) (files int, bytes int64) {
	ents, err := os.ReadDir(loc)
	if err != nil {
		return
	}
	for _, e := range ents {
		if e.IsDir() || !imgExt[strings.ToLower(filepath.Ext(e.Name()))] {
			continue
		}
		if info, err := e.Info(); err == nil {
			files++
			bytes += info.Size()
		}
	}
	return
}

func (a *App) stats() *Stats {
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	if a.statsData != nil && time.Since(a.statsAt) < 20*time.Second {
		return a.statsData
	}
	cfg := a.cfgSnap()
	st := &Stats{Presets: []PresetStat{}}
	seen := map[string]bool{}
	a.mu.Lock()
	hist := append([]*Job(nil), a.history...)
	a.mu.Unlock()
	for _, p := range cfg.Presets {
		loc := a.locationFor(cfg, p)
		ps := PresetStat{ID: p.ID, Location: loc}
		ps.Files, ps.Bytes = scanDir(loc)
		if _, err := os.Stat(filepath.Join(loc, "downloaded.txt")); err == nil {
			ps.Tracked = a.getSet(loc).size()
		}
		for i := len(hist) - 1; i >= 0; i-- {
			if hist[i].PresetID == p.ID {
				t := hist[i].EndedAt
				ps.LastRun, ps.LastStatus = &t, hist[i].Status
				break
			}
		}
		if !seen[loc] {
			seen[loc] = true
			st.TotalFiles += ps.Files
			st.TotalBytes += ps.Bytes
		}
		st.Presets = append(st.Presets, ps)
	}
	st.DiskFree, st.DiskTotal = diskUsage(cfg.BaseDir)
	st.RunsTotal = len(hist)
	for _, j := range hist {
		if time.Since(j.EndedAt) < 24*time.Hour {
			st.Runs24h++
			st.New24h += j.Downloaded
			st.Bytes24h += j.Bytes
		}
	}
	a.statsData, a.statsAt = st, time.Now()
	return st
}

type galleryItem struct {
	Preset string `json:"preset"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	MTime  int64  `json:"mtime"`
	URL    string `json:"url"`
}

func (a *App) handleGallery(w http.ResponseWriter, r *http.Request) {
	cfg := a.cfgSnap()
	want := r.URL.Query().Get("preset")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 300 {
		limit = 60
	}
	var all []galleryItem
	seen := map[string]bool{}
	for _, p := range cfg.Presets {
		if want != "" && want != "all" && want != p.ID {
			continue
		}
		loc := a.locationFor(cfg, p)
		if seen[loc] {
			continue
		}
		seen[loc] = true
		ents, err := os.ReadDir(loc)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() || !imgExt[strings.ToLower(filepath.Ext(e.Name()))] {
				continue
			}
			if info, err := e.Info(); err == nil {
				all = append(all, galleryItem{p.ID, e.Name(), info.Size(), info.ModTime().UnixMilli(), "/files/" + p.ID + "/" + e.Name()})
			}
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].MTime > all[j].MTime })
	total := len(all)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	writeJSON(w, 200, map[string]any{"total": total, "items": all[offset:end]})
}

func (a *App) presetFile(r *http.Request) (string, bool) {
	p, ok := a.findPreset(r.PathValue("preset"))
	if !ok {
		return "", false
	}
	name := r.PathValue("name")
	if name != filepath.Base(name) || strings.HasPrefix(name, ".") || !imgExt[strings.ToLower(filepath.Ext(name))] {
		return "", false
	}
	return filepath.Join(a.locationFor(a.cfgSnap(), p), name), true
}

func (a *App) handleFile(w http.ResponseWriter, r *http.Request) {
	f, ok := a.presetFile(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, f)
}

func (a *App) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	f, ok := a.presetFile(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := os.Remove(f); err != nil {
		jerr(w, 500, err)
		return
	}
	a.hub.Log("", "warn", "🗑 Deleted %s (stays in downloaded.txt so it won't come back)", filepath.Base(f))
	a.statsMu.Lock()
	a.statsData = nil
	a.statsMu.Unlock()
	writeJSON(w, 200, map[string]bool{"ok": true})
}
