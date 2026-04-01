package relay

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relayv1 "github.com/yuanyp8/harbor-relay/gen/proto/relay/v1"
	callbackmod "github.com/yuanyp8/harbor-relay/internal/callback"
	"github.com/yuanyp8/harbor-relay/internal/config"
)

func TestProcessNotificationQueueOneMsgRateLimitReschedules(t *testing.T) {
	serverCalls := 0
	robotServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalls++
		_, _ = io.WriteString(w, `{"msg":"群机器人发消息需要相隔1分钟","code":10002,"success":false}`)
	}))
	defer robotServer.Close()

	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}

	now := time.Now()
	task := &Task{
		ID:               "task-rate-limit",
		EventID:          "event-rate-limit",
		Channel:          "team-a",
		SiteName:         "team-a",
		SourceRegistry:   "registry.example.com:9443",
		Repository:       "team-a/registry-photon",
		Digest:           "sha256:ratelimit",
		Tags:             []string{"v1"},
		TargetRegistry:   "registry.example.com:9443",
		TargetRepository: "team-a-dr/registry-photon",
		Status:           relayv1.TaskStatus_TASK_STATUS_PENDING,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := store.AddTasks([]*Task{task}); err != nil {
		t.Fatalf("add task failed: %v", err)
	}

	cfg := config.RelayConfig{
		Targets: []config.TargetConfig{
			{
				Name:           "team-a",
				SiteName:       "team-a",
				TargetRegistry: "registry.example.com:9443",
				Notifications: []config.NotificationConfig{
					{
						Name:          "ops-group",
						Type:          "onemsg_robot",
						Endpoint:      robotServer.URL,
						RobotKey:      "robot-key",
						Events:        []string{"queued"},
						MinInterval:   time.Minute,
						RetryInterval: 10 * time.Second,
					},
				},
			},
		},
	}
	service := NewService(cfg, store, testLogger())

	if err := service.notifyTaskEvent(context.Background(), task, callbackmod.EventQueued); err != nil {
		t.Fatalf("queue notification failed: %v", err)
	}
	if err := service.processNotificationQueueOnce(context.Background()); err != nil {
		t.Fatalf("process queue failed: %v", err)
	}
	if serverCalls != 1 {
		t.Fatalf("expected 1 robot request, got %d", serverCalls)
	}

	jobs := store.ListNotificationJobs()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 queued notification job, got %d", len(jobs))
	}
	if jobs[0].Status != NotificationJobStatusRetrying {
		t.Fatalf("expected retrying status, got %s", jobs[0].Status)
	}
	if jobs[0].NextAttemptAt.Before(time.Now().Add(59 * time.Second)) {
		t.Fatalf("expected next attempt about one minute later, got %s", jobs[0].NextAttemptAt)
	}

	state, ok := store.GetNotificationChannelState("team-a::ops-group")
	if !ok {
		t.Fatal("expected channel state to exist")
	}
	if state.NextAllowedAt.Before(time.Now().Add(59 * time.Second)) {
		t.Fatalf("expected channel cooldown about one minute later, got %s", state.NextAllowedAt)
	}
}

func TestProcessNotificationQueue_MultipleRobotsAreIndependent(t *testing.T) {
	var firstRobotMsgs []string
	var secondRobotMsgs []string

	firstRobot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstRobotMsgs = append(firstRobotMsgs, r.URL.Query().Get("msg"))
		_, _ = io.WriteString(w, `{"success":true}`)
	}))
	defer firstRobot.Close()

	secondRobot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondRobotMsgs = append(secondRobotMsgs, r.URL.Query().Get("msg"))
		_, _ = io.WriteString(w, `{"success":true}`)
	}))
	defer secondRobot.Close()

	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}

	now := time.Now()
	task := &Task{
		ID:               "task-multi-robot",
		EventID:          "event-multi-robot",
		Channel:          "team-a",
		SiteName:         "team-a",
		SourceRegistry:   "registry.example.com:9443",
		Repository:       "team-a/registry-photon",
		Digest:           "sha256:multi",
		Tags:             []string{"v1"},
		TargetRegistry:   "registry.example.com:9443",
		TargetRepository: "team-a-dr/registry-photon",
		Status:           relayv1.TaskStatus_TASK_STATUS_PENDING,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := store.AddTasks([]*Task{task}); err != nil {
		t.Fatalf("add task failed: %v", err)
	}

	cfg := config.RelayConfig{
		Targets: []config.TargetConfig{
			{
				Name:           "team-a",
				SiteName:       "team-a",
				TargetRegistry: "registry.example.com:9443",
				Notifications: []config.NotificationConfig{
					{
						Name:          "ops-group",
						Type:          "onemsg_robot",
						Endpoint:      firstRobot.URL,
						RobotKey:      "ops-key",
						Events:        []string{"queued"},
						MinInterval:   time.Minute,
						RetryInterval: 5 * time.Second,
						TitlePrefix:   "Ops Steward",
					},
					{
						Name:          "app-group",
						Type:          "onemsg_robot",
						Endpoint:      secondRobot.URL,
						RobotKey:      "app-key",
						Events:        []string{"queued"},
						MinInterval:   time.Minute,
						RetryInterval: 5 * time.Second,
						TitlePrefix:   "App Steward",
					},
				},
			},
		},
	}
	service := NewService(cfg, store, testLogger())

	if err := service.notifyTaskEvent(context.Background(), task, callbackmod.EventQueued); err != nil {
		t.Fatalf("queue notification failed: %v", err)
	}
	if err := service.processNotificationQueueOnce(context.Background()); err != nil {
		t.Fatalf("process queue failed: %v", err)
	}

	if len(firstRobotMsgs) != 1 || len(secondRobotMsgs) != 1 {
		t.Fatalf("expected both robots to receive one message, got first=%d second=%d", len(firstRobotMsgs), len(secondRobotMsgs))
	}
	if !strings.Contains(firstRobotMsgs[0], "Ops Steward") {
		t.Fatalf("unexpected first robot message: %s", firstRobotMsgs[0])
	}
	if !strings.Contains(secondRobotMsgs[0], "App Steward") {
		t.Fatalf("unexpected second robot message: %s", secondRobotMsgs[0])
	}
}
