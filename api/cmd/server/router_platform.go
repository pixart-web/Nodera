package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/ops"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/httpserver"
)

func (d apiDeps) mountPlatform(r chi.Router) {
	r.Get("/dashboard", d.handleDashboard)
	r.Get("/search", d.handleSearch)
	r.Get("/feature-flags", d.handleListFlags)
	r.Put("/feature-flags/{key}", d.handleSetFlag)
	r.Get("/operations/{id}/stream", d.handleOperationStream)
	r.Get("/events", d.handleEvents)

	// AI operations layer: the model proposes, humans approve and run.
	r.Get("/ai/plans", d.handleListAIPlans)
	r.Post("/ai/plans", d.handleProposeAIPlan)
	r.Get("/ai/plans/{id}", d.handleGetAIPlan)
	r.Post("/ai/plans/{id}/approve", d.handleDecideAIPlan(true))
	r.Post("/ai/plans/{id}/reject", d.handleDecideAIPlan(false))
	r.Post("/ai/plans/{id}/steps/{n}/run", d.handleRunAIPlanStep)
}

func (d apiDeps) handleListAIPlans(w http.ResponseWriter, r *http.Request) {
	p := httpserver.ParsePagination(r)
	items, err := d.aiplans.List(r.Context(), mustAuthContext(r), p.Limit+1, p.Offset)
	replyPage(w, r, p, items, err)
}

func (d apiDeps) handleProposeAIPlan(w http.ResponseWriter, r *http.Request) {
	if d.aiChatRate != nil && !d.aiChatRate.Allow(clientIP(r)+":plan") {
		httpserver.WriteError(w, r, apierr.New(apierr.CodeRateLimited, "too many AI requests"))
		return
	}
	var in struct {
		ProfileKey string     `json:"profile_key"`
		ProjectID  *uuid.UUID `json:"project_id"`
		Goal       string     `json:"goal"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.ProfileKey == "" {
		in.ProfileKey = "default"
	}
	p, err := d.aiplans.Propose(r.Context(), mustAuthContext(r), in.ProfileKey, in.ProjectID, in.Goal)
	reply(w, r, http.StatusCreated, p, err)
}

func (d apiDeps) handleGetAIPlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	p, err := d.aiplans.Get(r.Context(), mustAuthContext(r), id)
	reply(w, r, http.StatusOK, p, err)
}

func (d apiDeps) handleDecideAIPlan(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		p, err := d.aiplans.Decide(r.Context(), mustAuthContext(r), id, approve)
		reply(w, r, http.StatusOK, p, err)
	}
}

func (d apiDeps) handleRunAIPlanStep(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	n := queryIntParam(chi.URLParam(r, "n"))
	p, err := d.aiplans.RunStep(r.Context(), mustAuthContext(r), id, n)
	reply(w, r, http.StatusAccepted, p, err)
}

func queryIntParam(s string) int {
	n := -1
	fmt.Sscan(s, &n)
	return n
}

func (d apiDeps) handleDashboard(w http.ResponseWriter, r *http.Request) {
	s, err := d.dashboard.Summary(r.Context(), mustAuthContext(r))
	reply(w, r, http.StatusOK, s, err)
}

func (d apiDeps) handleSearch(w http.ResponseWriter, r *http.Request) {
	hits, err := d.dashboard.Search(r.Context(), mustAuthContext(r), r.URL.Query().Get("q"))
	reply(w, r, http.StatusOK, map[string]any{"hits": hits}, err)
}

func (d apiDeps) handleListFlags(w http.ResponseWriter, r *http.Request) {
	f, err := d.flags.List(r.Context(), mustAuthContext(r))
	reply(w, r, http.StatusOK, f, err)
}

// handleSetFlag sets (enabled true/false) or clears (enabled null) this
// organisation's override.
func (d apiDeps) handleSetFlag(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	noContent(w, r, d.flags.SetOverride(r.Context(), mustAuthContext(r), chi.URLParam(r, "key"), in.Enabled))
}

// ---- server-sent events ----

type sseWriter struct {
	w http.ResponseWriter
	f http.Flusher
}

func newSSE(w http.ResponseWriter, r *http.Request) (*sseWriter, bool) {
	f, ok := w.(http.Flusher)
	if !ok {
		httpserver.WriteError(w, r, apierr.New(apierr.CodeInternal, "streaming is not supported by this connection"))
		return nil, false
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no") // keep reverse proxies (Traefik/nginx) from buffering the stream
	w.WriteHeader(http.StatusOK)
	f.Flush()
	return &sseWriter{w: w, f: f}, true
}

func (s *sseWriter) send(event, id string, v any) {
	b, _ := json.Marshal(v)
	if id != "" {
		fmt.Fprintf(s.w, "id: %s\n", id)
	}
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, b)
	s.f.Flush()
}

func (s *sseWriter) comment(c string) {
	fmt.Fprintf(s.w, ": %s\n\n", c)
	s.f.Flush()
}

// handleOperationStream streams an operation's steps, logs and status until it
// reaches a terminal state. Reconnecting clients resume with Last-Event-ID
// (the last log id they saw). Authorisation is the normal per-request check, so
// a stream can only ever show an operation the caller may read.
func (d apiDeps) handleOperationStream(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	ac := mustAuthContext(r)
	v, err := d.ops.Get(r.Context(), ac, id)
	if err != nil {
		httpserver.WriteError(w, r, err)
		return
	}
	var lastLog int64
	fmt.Sscan(r.Header.Get("Last-Event-ID"), &lastLog)
	if lastLog == 0 {
		lastLog = int64(queryInt(r, "after", 0))
	}
	sse, ok := newSSE(w, r)
	if !ok {
		return
	}
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	beat := time.NewTicker(15 * time.Second)
	defer beat.Stop()
	lastStatus, lastProgress := "", -1.0
	for {
		logs, _ := d.ops.Logs(r.Context(), ac, id, lastLog, 200)
		for _, l := range logs {
			sse.send("log", fmt.Sprint(l.ID), l)
			lastLog = l.ID
		}
		cur, err := d.ops.Get(r.Context(), ac, id)
		if err == nil {
			v = cur
		}
		if v.Status != lastStatus || v.Progress != lastProgress {
			steps, _ := d.ops.Steps(r.Context(), ac, id)
			sse.send("status", "", map[string]any{"operation": v, "steps": steps})
			lastStatus, lastProgress = v.Status, v.Progress
		}
		switch v.Status {
		case "succeeded", "failed", "cancelled":
			if len(logs) == 0 { // drain remaining logs once more before ending
				sse.send("end", "", map[string]string{"status": v.Status})
				return
			}
			continue
		}
		select {
		case <-r.Context().Done():
			return
		case <-beat.C:
			sse.comment("keep-alive")
		case <-tick.C:
		}
	}
}

// handleEvents is the organisation event stream: unread-notification count
// and operation status changes. It is a low-frequency poll on the server side
// (2s) multiplexed onto one connection per browser tab.
func (d apiDeps) handleEvents(w http.ResponseWriter, r *http.Request) {
	ac := mustAuthContext(r)
	sse, ok := newSSE(w, r)
	if !ok {
		return
	}
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	lastUnread := -1
	seen := map[string]string{}
	started := time.Now().Add(-time.Minute)
	for {
		if n, err := d.notifications.UnreadCount(r.Context(), ac); err == nil && n != lastUnread {
			sse.send("notifications", "", map[string]int{"unread": n})
			lastUnread = n
		}
		if items, err := d.ops.List(r.Context(), ac, uuidNil, 20, 0); err == nil {
			for _, o := range items {
				if o.CreatedAt.Before(started) && o.Status != "running" && seen[o.ID.String()] == "" {
					seen[o.ID.String()] = o.Status
					continue
				}
				if seen[o.ID.String()] != o.Status {
					seen[o.ID.String()] = o.Status
					sse.send("operation", "", o)
				}
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
	}
}

var uuidNil = uuid.Nil
var _ = ops.SubmitInput{}
