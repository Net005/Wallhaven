package main

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

type LogLine struct {
	T   int64  `json:"t"`
	Lvl string `json:"lvl"`
	Job string `json:"job,omitempty"`
	Msg string `json:"msg"`
}

type sseMsg struct {
	Event string
	Data  []byte
}

type Hub struct {
	mu   sync.Mutex
	subs map[chan sseMsg]struct{}
	logs []LogLine
}

const logCap = 3000

func NewHub() *Hub { return &Hub{subs: map[chan sseMsg]struct{}{}} }

func (h *Hub) Sub() chan sseMsg {
	ch := make(chan sseMsg, 1024)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) Unsub(ch chan sseMsg) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

func (h *Hub) Publish(event string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	m := sseMsg{event, b}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- m:
		default: // slow client: drop
		}
	}
}

// Log records a line in the ring buffer and pushes it to all live viewers.
// Levels: head, info, ok, warn, err, skip
func (h *Hub) Log(job, lvl, format string, args ...any) {
	l := LogLine{T: time.Now().UnixMilli(), Lvl: lvl, Job: job, Msg: fmt.Sprintf(format, args...)}
	h.mu.Lock()
	h.logs = append(h.logs, l)
	if len(h.logs) > logCap {
		h.logs = append([]LogLine(nil), h.logs[len(h.logs)-logCap:]...)
	}
	h.mu.Unlock()
	if lvl != "skip" {
		log.Printf("[%s] %s", lvl, l.Msg)
	}
	h.Publish("log", l)
}

func (h *Hub) Backlog() []LogLine {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]LogLine(nil), h.logs...)
}

func (h *Hub) ClearLogs() {
	h.mu.Lock()
	h.logs = nil
	h.mu.Unlock()
	h.Publish("clear", true)
}
