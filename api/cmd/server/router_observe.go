package main

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/logs"
	"github.com/nodera/nodera/internal/monitoring"
	"github.com/nodera/nodera/internal/notifications"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/httpserver"
)

func (d apiDeps) mountObserve(r chi.Router) {
	// monitors, metrics, rules
	r.Get("/monitors", d.handleListMonitors)
	r.Post("/monitors", d.handleCreateMonitor)
	r.Patch("/monitors/{id}", d.handleUpdateMonitor)
	r.Delete("/monitors/{id}", d.handleDeleteMonitor)
	r.Post("/monitors/{id}/run", d.handleRunMonitor)
	r.Get("/metrics/{metric}", d.handleMetrics)
	r.Get("/alert-rules", d.handleListRules)
	r.Post("/alert-rules", d.handleCreateRule)
	r.Delete("/alert-rules/{id}", d.handleDeleteRule)

	// incidents
	r.Get("/incidents", d.handleListIncidents)
	r.Get("/incidents/{id}", d.handleGetIncident)
	r.Post("/incidents/{id}/{action}", d.handleIncidentAction)

	// notifications
	r.Get("/notifications", d.handleListNotifications)
	r.Get("/notifications/unread-count", d.handleUnreadCount)
	r.Post("/notifications/read", d.handleMarkRead)
	r.Get("/notification-channels", d.handleListChannels)
	r.Post("/notification-channels", d.handleCreateChannel)
	r.Delete("/notification-channels/{id}", d.handleDeleteChannel)

	// logs & retention
	r.Get("/logs", d.handleQueryLogs)
	r.Get("/projects/{id}/container-logs", d.handleContainerLogs)
	r.Get("/retention", d.handleGetRetention)
	r.Put("/retention/{resource}", d.handleSetRetention)
}

func (d apiDeps) handleListMonitors(w http.ResponseWriter, r *http.Request) {
	p := httpserver.ParsePagination(r)
	items, err := d.monitoring.ListMonitors(r.Context(), mustAuthContext(r), queryUUID(r, "project_id"), p.Limit+1, p.Offset)
	replyPage(w, r, p, items, err)
}

func (d apiDeps) handleCreateMonitor(w http.ResponseWriter, r *http.Request) {
	var in monitoring.MonitorInput
	if !decodeJSON(w, r, &in) {
		return
	}
	m, err := d.monitoring.CreateMonitor(r.Context(), mustAuthContext(r), in)
	reply(w, r, http.StatusCreated, m, err)
}

func (d apiDeps) handleUpdateMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Enabled         *bool `json:"enabled"`
		IntervalSeconds *int  `json:"interval_seconds"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	m, err := d.monitoring.UpdateMonitor(r.Context(), mustAuthContext(r), id, in.Enabled, in.IntervalSeconds)
	reply(w, r, http.StatusOK, m, err)
}

func (d apiDeps) handleDeleteMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	noContent(w, r, d.monitoring.DeleteMonitor(r.Context(), mustAuthContext(r), id))
}

func (d apiDeps) handleRunMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	m, err := d.monitoring.RunNow(r.Context(), mustAuthContext(r), id)
	reply(w, r, http.StatusOK, m, err)
}

func (d apiDeps) handleMetrics(w http.ResponseWriter, r *http.Request) {
	var since time.Time
	if v := r.URL.Query().Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			httpserver.WriteError(w, r, apierr.Validation("since must be RFC 3339"))
			return
		}
		since = t
	}
	s, err := d.monitoring.Metrics(r.Context(), mustAuthContext(r), chi.URLParam(r, "metric"), queryUUID(r, "node_id"), queryUUID(r, "project_id"), since, queryInt(r, "limit", 200))
	reply(w, r, http.StatusOK, s, err)
}

func (d apiDeps) handleListRules(w http.ResponseWriter, r *http.Request) {
	items, err := d.monitoring.ListRules(r.Context(), mustAuthContext(r))
	reply(w, r, http.StatusOK, items, err)
}

func (d apiDeps) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	var in monitoring.RuleInput
	if !decodeJSON(w, r, &in) {
		return
	}
	rule, err := d.monitoring.CreateRule(r.Context(), mustAuthContext(r), in)
	reply(w, r, http.StatusCreated, rule, err)
}

func (d apiDeps) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	noContent(w, r, d.monitoring.DeleteRule(r.Context(), mustAuthContext(r), id))
}

func (d apiDeps) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	p := httpserver.ParsePagination(r)
	items, err := d.monitoring.ListIncidents(r.Context(), mustAuthContext(r), r.URL.Query().Get("status"), queryUUID(r, "project_id"), p.Limit+1, p.Offset)
	replyPage(w, r, p, items, err)
}

func (d apiDeps) handleGetIncident(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	i, err := d.monitoring.GetIncident(r.Context(), mustAuthContext(r), id)
	reply(w, r, http.StatusOK, i, err)
}

func (d apiDeps) handleIncidentAction(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	if r.ContentLength > 0 && !decodeJSON(w, r, &in) {
		return
	}
	i, err := d.monitoring.Transition(r.Context(), mustAuthContext(r), id, chi.URLParam(r, "action"), in.Note)
	reply(w, r, http.StatusOK, i, err)
}

func (d apiDeps) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	p := httpserver.ParsePagination(r)
	items, err := d.notifications.List(r.Context(), mustAuthContext(r), r.URL.Query().Get("unread") == "true", p.Limit+1, p.Offset)
	replyPage(w, r, p, items, err)
}

func (d apiDeps) handleUnreadCount(w http.ResponseWriter, r *http.Request) {
	n, err := d.notifications.UnreadCount(r.Context(), mustAuthContext(r))
	reply(w, r, http.StatusOK, map[string]int{"unread": n}, err)
}

func (d apiDeps) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []uuid.UUID `json:"ids"`
		All bool        `json:"all"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if len(in.IDs) > 500 {
		httpserver.WriteError(w, r, apierr.Validation("too many ids"))
		return
	}
	if in.All {
		noContent(w, r, d.notifications.MarkAllRead(r.Context(), mustAuthContext(r)))
		return
	}
	noContent(w, r, d.notifications.MarkRead(r.Context(), mustAuthContext(r), in.IDs))
}

func (d apiDeps) handleListChannels(w http.ResponseWriter, r *http.Request) {
	items, err := d.notifications.ListChannels(r.Context(), mustAuthContext(r))
	reply(w, r, http.StatusOK, items, err)
}

func (d apiDeps) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	var in notifications.ChannelInput
	if !decodeJSON(w, r, &in) {
		return
	}
	c, err := d.notifications.CreateChannel(r.Context(), mustAuthContext(r), in)
	reply(w, r, http.StatusCreated, c, err)
}

func (d apiDeps) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	noContent(w, r, d.notifications.DeleteChannel(r.Context(), mustAuthContext(r), id))
}

func timeParam(r *http.Request, key string) time.Time {
	if t, err := time.Parse(time.RFC3339, r.URL.Query().Get(key)); err == nil {
		return t
	}
	return time.Time{}
}

func (d apiDeps) handleQueryLogs(w http.ResponseWriter, r *http.Request) {
	p := httpserver.ParsePagination(r)
	q := r.URL.Query()
	items, err := d.logs.Query(r.Context(), mustAuthContext(r), logs.Filter{
		ProjectID: queryUUID(r, "project_id"), Source: q.Get("source"), Level: q.Get("level"), Query: q.Get("q"),
		From: timeParam(r, "from"), To: timeParam(r, "to"), Limit: p.Limit + 1, Offset: p.Offset,
	})
	replyPage(w, r, p, items, err)
}

func (d apiDeps) handleContainerLogs(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	lines, err := d.logs.ContainerLogs(r.Context(), mustAuthContext(r), id, queryInt(r, "tail", 200))
	reply(w, r, http.StatusOK, map[string]any{"lines": lines}, err)
}

func (d apiDeps) handleGetRetention(w http.ResponseWriter, r *http.Request) {
	items, err := d.logs.Retention(r.Context(), mustAuthContext(r))
	reply(w, r, http.StatusOK, items, err)
}

func (d apiDeps) handleSetRetention(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RetentionDays int `json:"retention_days"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	noContent(w, r, d.logs.SetRetention(r.Context(), mustAuthContext(r), chi.URLParam(r, "resource"), in.RetentionDays))
}
