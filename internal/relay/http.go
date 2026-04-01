package relay

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

func (s *Service) HTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		s.logger.Debug("healthz request received", "remote_addr", r.RemoteAddr, "user_agent", r.UserAgent())
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"service": s.cfg.ServiceName,
		})
	})
	for _, webhook := range s.cfg.Webhooks {
		if webhook.IsEnabled() {
			mux.HandleFunc(webhook.Path, s.HandleWebhook)
		}
	}
	mux.HandleFunc("/api/v1/tasks", s.handleTasks)
	mux.HandleFunc("/api/v1/tasks/", s.handleTaskByID)
	mux.HandleFunc("/api/v1/agents", s.handleAgents)
	mux.HandleFunc("/api/v1/notification-jobs", s.handleNotificationJobs)
	return s.withAccessLog(mux)
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *loggingResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *loggingResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += n
	return n, err
}

func (s *Service) withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := &loggingResponseWriter{ResponseWriter: w}
		next.ServeHTTP(lrw, r)
		if lrw.status == 0 {
			lrw.status = http.StatusOK
		}
		s.logger.Debug("http request completed",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("query", r.URL.RawQuery),
			slog.String("remote_addr", r.RemoteAddr),
			slog.String("user_agent", r.UserAgent()),
			slog.Int("status", lrw.status),
			slog.Int("bytes", lrw.bytes),
			slog.Duration("duration", time.Since(start)),
		)
	})
}

func (s *Service) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.logger.Debug("tasks list requested")
	writeJSON(w, http.StatusOK, map[string]any{
		"items": s.store.ListTasks(),
	})
}

func (s *Service) handleTaskByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/")
	task, ok := s.store.GetTask(id)
	if !ok {
		s.logger.Warn("task detail requested but task was not found", "task_id", id)
		http.NotFound(w, r)
		return
	}
	s.logger.Debug("task detail requested", "task_id", id)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(task)
}

func (s *Service) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.logger.Debug("agents list requested")
	writeJSON(w, http.StatusOK, map[string]any{
		"items": s.store.ListAgents(),
	})
}

func (s *Service) handleNotificationJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.logger.Debug("notification jobs list requested")
	writeJSON(w, http.StatusOK, map[string]any{
		"items": s.store.ListNotificationJobs(),
	})
}
