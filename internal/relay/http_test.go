package relay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relayv1 "github.com/yuanyp8/harbor-relay/gen/proto/relay/v1"
	"github.com/yuanyp8/harbor-relay/internal/config"
)

func TestHTTPHandler_HealthTasksAgents(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}
	now := time.Now()
	if err := store.AddTasks([]*Task{
		{
			ID:        "task-1",
			EventID:   "event-1",
			Channel:   "db-core",
			SiteName:  "dc1",
			Status:    relayv1.TaskStatus_TASK_STATUS_PENDING,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}); err != nil {
		t.Fatalf("add tasks failed: %v", err)
	}
	if err := store.UpsertAgent(&Agent{
		AgentID:     "agent-1",
		SiteName:    "dc1",
		Channels:    []string{"db-core"},
		ConnectedAt: now,
		LastSeenAt:  now,
	}); err != nil {
		t.Fatalf("upsert agent failed: %v", err)
	}

	service := NewService(config.RelayConfig{ServiceName: "harbor-relay"}, store, testLogger())
	handler := service.HTTPHandler()

	rrHealth := httptest.NewRecorder()
	reqHealth := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler.ServeHTTP(rrHealth, reqHealth)
	if rrHealth.Code != http.StatusOK {
		t.Fatalf("unexpected health status: %d", rrHealth.Code)
	}

	rrTasks := httptest.NewRecorder()
	reqTasks := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	handler.ServeHTTP(rrTasks, reqTasks)
	if rrTasks.Code != http.StatusOK {
		t.Fatalf("unexpected tasks status: %d", rrTasks.Code)
	}
	var tasksResp map[string][]Task
	if err := json.Unmarshal(rrTasks.Body.Bytes(), &tasksResp); err != nil {
		t.Fatalf("unmarshal tasks response failed: %v", err)
	}
	if len(tasksResp["items"]) != 1 {
		t.Fatalf("expected 1 task item, got %d", len(tasksResp["items"]))
	}

	rrAgents := httptest.NewRecorder()
	reqAgents := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	handler.ServeHTTP(rrAgents, reqAgents)
	if rrAgents.Code != http.StatusOK {
		t.Fatalf("unexpected agents status: %d", rrAgents.Code)
	}
	var agentsResp map[string][]Agent
	if err := json.Unmarshal(rrAgents.Body.Bytes(), &agentsResp); err != nil {
		t.Fatalf("unmarshal agents response failed: %v", err)
	}
	if len(agentsResp["items"]) != 1 {
		t.Fatalf("expected 1 agent item, got %d", len(agentsResp["items"]))
	}
}

func TestHTTPHandler_HealthzProducesLogs(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}

	logger, logBuf := bufferedTestLogger()
	service := NewService(config.RelayConfig{ServiceName: "harbor-relay"}, store, logger)
	handler := service.HTTPHandler()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected health status: %d", rr.Code)
	}

	logs := logBuf.String()
	for _, want := range []string{
		"healthz request received",
		"http request completed",
		"path=/healthz",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %q, got:\n%s", want, logs)
		}
	}
}
