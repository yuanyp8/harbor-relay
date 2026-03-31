package relay

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	relayv1 "github.com/yuanyp8/harbor-relay/gen/proto/relay/v1"
	"github.com/yuanyp8/harbor-relay/internal/config"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestHandleWebhook_QueuesTasksByRouteAndChannel 验证最核心的主链路：
// Harbor webhook -> route 命中 -> 映射 channel -> 展开成多个 target task。
func TestHandleWebhook_QueuesTasksByRouteAndChannel(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}

	cfg := config.RelayConfig{
		ServiceName:    "harbor-relay",
		SourceRegistry: "image.hm.metavarse.tech:9443",
		Webhooks: []config.WebhookConfig{
			{
				Name:          "default",
				Path:          "/api/v1/harbor/webhook",
				Authorization: "Bearer test-secret",
			},
		},
		Routes: []config.RouteConfig{
			{
				Name:               "mysql-core",
				Channel:            "db-core",
				RepositoryPatterns: []string{"kube4/mysql"},
				TargetSites:        []string{"dc1", "dc2"},
			},
		},
		Targets: []config.TargetConfig{
			{
				Name:             "dc1",
				SiteName:         "dc1",
				TargetRegistry:   "sealos.hub:5000",
				RepositoryPrefix: "",
			},
			{
				Name:             "dc2",
				SiteName:         "dc2",
				TargetRegistry:   "harbor.remote.example.com",
				RepositoryPrefix: "mirror",
			},
		},
	}

	service := NewService(cfg, store, testLogger())

	payload := map[string]any{
		"type":     "PUSH_ARTIFACT",
		"operator": "admin",
		"event_data": map[string]any{
			"repository": map[string]any{
				"repo_full_name": "kube4/mysql",
			},
			"resources": []map[string]any{
				{
					"digest": "sha256:1111111111111111111111111111111111111111111111111111111111111111",
					"tag":    "8.0.45",
				},
				{
					"digest": "sha256:1111111111111111111111111111111111111111111111111111111111111111",
					"tag":    "latest",
				},
			},
		},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/harbor/webhook", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret")
	rr := httptest.NewRecorder()

	service.HandleWebhook(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	tasks := store.ListTasks()
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}

	for _, task := range tasks {
		if task.Channel != "db-core" {
			t.Fatalf("unexpected channel: %s", task.Channel)
		}
		if task.Repository != "kube4/mysql" {
			t.Fatalf("unexpected repository: %s", task.Repository)
		}
		if task.SourceRegistry != "image.hm.metavarse.tech:9443" {
			t.Fatalf("unexpected source registry: %s", task.SourceRegistry)
		}
		if len(task.Tags) != 2 {
			t.Fatalf("expected 2 tags, got %d", len(task.Tags))
		}
		if task.SiteName == "dc1" && task.TargetRepository != "kube4/mysql" {
			t.Fatalf("unexpected dc1 target repository: %s", task.TargetRepository)
		}
		if task.SiteName == "dc2" && task.TargetRepository != "mirror/kube4/mysql" {
			t.Fatalf("unexpected dc2 target repository: %s", task.TargetRepository)
		}
		if task.Metadata["route_name"] != "mysql-core" {
			t.Fatalf("unexpected route_name metadata: %v", task.Metadata)
		}
	}
}

// TestHandleWebhook_DuplicateEventIgnored 验证重复 body 不会重复入队。
func TestHandleWebhook_DuplicateEventIgnored(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}

	cfg := config.RelayConfig{
		Webhooks: []config.WebhookConfig{
			{Name: "default", Path: "/api/v1/harbor/webhook"},
		},
		Routes: []config.RouteConfig{
			{
				Name:               "mysql-core",
				Channel:            "db-core",
				RepositoryPatterns: []string{"kube4/mysql"},
				TargetSites:        []string{"dc1"},
			},
		},
		Targets: []config.TargetConfig{
			{Name: "dc1", SiteName: "dc1", TargetRegistry: "sealos.hub:5000"},
		},
	}
	service := NewService(cfg, store, testLogger())

	body := []byte(`{"type":"PUSH_ARTIFACT","event_data":{"repository":{"repo_full_name":"kube4/mysql"},"resources":[{"digest":"sha256:aaaa","tag":"8.0.45"}]}}`)

	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/harbor/webhook", bytes.NewReader(body))
	rr1 := httptest.NewRecorder()
	service.HandleWebhook(rr1, req1)

	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/harbor/webhook", bytes.NewReader(body))
	rr2 := httptest.NewRecorder()
	service.HandleWebhook(rr2, req2)

	if got := len(store.ListTasks()); got != 1 {
		t.Fatalf("expected 1 task after duplicate webhook, got %d", got)
	}
	if rr2.Code != http.StatusAccepted {
		t.Fatalf("unexpected second status: %d", rr2.Code)
	}
	if !bytes.Contains(rr2.Body.Bytes(), []byte(`"duplicate"`)) {
		t.Fatalf("expected duplicate response, got %s", rr2.Body.String())
	}
}

// TestAssignNextTask_RespectsChannelSubscription 验证调度时会同时校验站点和频道。
func TestAssignNextTask_RespectsChannelSubscription(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}

	now := time.Now()
	err = store.AddTasks([]*Task{
		{
			ID:        "task-db",
			EventID:   "event-1",
			Channel:   "db-core",
			SiteName:  "dc1",
			Status:    relayv1.TaskStatus_TASK_STATUS_PENDING,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "task-ai",
			EventID:   "event-2",
			Channel:   "ai-platform",
			SiteName:  "dc1",
			Status:    relayv1.TaskStatus_TASK_STATUS_PENDING,
			CreatedAt: now.Add(time.Second),
			UpdatedAt: now.Add(time.Second),
		},
	})
	if err != nil {
		t.Fatalf("add tasks failed: %v", err)
	}

	err = store.UpsertAgent(&Agent{
		AgentID:     "agent-1",
		SiteName:    "dc1",
		Channels:    []string{"ai-platform"},
		ConnectedAt: now,
		LastSeenAt:  now,
	})
	if err != nil {
		t.Fatalf("upsert agent failed: %v", err)
	}

	task, err := store.AssignNextTask("dc1", []string{"ai-platform"}, "agent-1")
	if err != nil {
		t.Fatalf("assign task failed: %v", err)
	}
	if task == nil {
		t.Fatal("expected one assigned task, got nil")
	}
	if task.ID != "task-ai" {
		t.Fatalf("expected task-ai, got %s", task.ID)
	}
}
