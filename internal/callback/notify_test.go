package callback

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yuanyp8/harbor-relay/internal/config"
)

func TestFormatOneMsgMessage_IsPlainMultilineText(t *testing.T) {
	msg := formatOneMsgMessage("Yunnan Platform", TaskEvent{
		Event:                EventDone,
		TaskID:               "task-1",
		SiteName:             "team-a",
		Channel:              "team-a",
		Digest:               "sha256:test",
		Tags:                 []string{"v2.15.0"},
		SourceRefs:           []string{"registry.example.com:9443/team-a/registry-photon:v2.15.0@sha256:test"},
		TargetRefDescriptors: []string{"registry.example.com:9443/team-a-dr/registry-photon:v2.15.0@sha256:test"},
		Message:              "## done\n**all good**",
	})

	if !strings.Contains(msg, "\n") {
		t.Fatalf("expected multiline plain text message, got: %s", msg)
	}
	for _, forbidden := range []string{"**", "##"} {
		if strings.Contains(msg, forbidden) {
			t.Fatalf("expected message to remove %q, got: %s", forbidden, msg)
		}
	}
	if !strings.Contains(msg, "[Yunnan Platform] 镜像同步已完成") {
		t.Fatalf("unexpected title: %s", msg)
	}
	if !strings.Contains(msg, "说明: done all good") {
		t.Fatalf("unexpected normalized detail: %s", msg)
	}
}

func TestNotifier_SendOneMsgRobot(t *testing.T) {
	var gotRobotKey string
	var gotMsg string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRobotKey = r.URL.Query().Get("robotKey")
		gotMsg = r.URL.Query().Get("msg")
		_, _ = io.WriteString(w, `{"success":true}`)
	}))
	defer server.Close()

	notifier := NewNotifier([]config.TargetConfig{
		{
			SiteName: "team-a",
			Notifications: []config.NotificationConfig{
				{
					Name:        "ops-group",
					Type:        "onemsg_robot",
					Endpoint:    server.URL,
					RobotKey:    "replace-with-robot-key",
					Events:      []string{"queued", "done"},
					TitlePrefix: "Yunnan Platform",
				},
			},
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	err := notifier.Notify(context.Background(), "team-a", TaskEvent{
		Event:            EventQueued,
		TaskID:           "task-1",
		SiteName:         "team-a",
		Channel:          "team-a",
		Repository:       "team-a/registry-photon",
		Digest:           "sha256:test",
		Tags:             []string{"v2.15.0"},
		TargetRepository: "team-a-dr/registry-photon",
	})
	if err != nil {
		t.Fatalf("notify failed: %v", err)
	}
	if gotRobotKey != "replace-with-robot-key" {
		t.Fatalf("unexpected robot key: %s", gotRobotKey)
	}
	if !strings.Contains(gotMsg, "镜像同步已入队") {
		t.Fatalf("unexpected robot msg: %s", gotMsg)
	}
}

func TestNotifier_ExtractsRobotKeyFromEndpoint(t *testing.T) {
	var gotRobotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRobotKey = r.URL.Query().Get("robotKey")
		_, _ = io.WriteString(w, `{"success":true}`)
	}))
	defer server.Close()

	notifier := NewNotifier([]config.TargetConfig{
		{
			SiteName: "team-a",
			Notifications: []config.NotificationConfig{
				{
					Name:     "ops-group",
					Type:     "onemsg_robot",
					Endpoint: server.URL + "?robotKey=from-endpoint&msg=",
					Events:   []string{"done"},
				},
			},
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	channel, ok := notifier.GetChannel("team-a", "ops-group")
	if !ok {
		t.Fatal("expected notification channel to exist")
	}
	if err := notifier.Send(context.Background(), channel, TaskEvent{
		Event:    EventDone,
		TaskID:   "task-1",
		SiteName: "team-a",
	}); err != nil {
		t.Fatalf("send failed: %v", err)
	}
	if gotRobotKey != "from-endpoint" {
		t.Fatalf("unexpected robot key: %s", gotRobotKey)
	}
}

func TestNotifier_DefaultEvents_DoNotIncludePulling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected request for pulling event")
	}))
	defer server.Close()

	notifier := NewNotifier([]config.TargetConfig{
		{
			SiteName: "dc1",
			Notifications: []config.NotificationConfig{
				{
					Name:     "ops-group",
					Endpoint: server.URL,
					RobotKey: "key",
				},
			},
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := notifier.Notify(context.Background(), "dc1", TaskEvent{
		Event:    EventPulling,
		TaskID:   "task-1",
		SiteName: "dc1",
	}); err != nil {
		t.Fatalf("notify failed: %v", err)
	}
}

func TestNotifier_CallbackPendingAliasMatchesCallbackFailed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"success":true}`)
	}))
	defer server.Close()

	notifier := NewNotifier([]config.TargetConfig{
		{
			SiteName: "dc1",
			Notifications: []config.NotificationConfig{
				{
					Name:     "ops-group",
					Endpoint: server.URL,
					RobotKey: "key",
					Events:   []string{"callback_pending"},
				},
			},
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := notifier.Notify(context.Background(), "dc1", TaskEvent{
		Event:           EventCallbackFailed,
		TaskID:          "task-1",
		SiteName:        "dc1",
		CallbackMessage: "callback failed",
	}); err != nil {
		t.Fatalf("notify failed: %v", err)
	}
}

func TestNotifier_SendOneMsgRobot_RateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"msg":"群机器人发消息需要相隔1分钟","code":10002,"success":false}`)
	}))
	defer server.Close()

	notifier := NewNotifier([]config.TargetConfig{
		{
			SiteName: "dc1",
			Notifications: []config.NotificationConfig{
				{
					Name:        "ops-group",
					Type:        "onemsg_robot",
					Endpoint:    server.URL,
					RobotKey:    "key",
					MinInterval: time.Minute,
				},
			},
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	channel, ok := notifier.GetChannel("dc1", "ops-group")
	if !ok {
		t.Fatal("expected channel to exist")
	}

	err := notifier.Send(context.Background(), channel, TaskEvent{
		Event:    EventQueued,
		TaskID:   "task-1",
		SiteName: "dc1",
	})
	if err == nil {
		t.Fatal("expected rate limit error")
	}

	delay, retryable := RetryDecision(err, 30*time.Second)
	if !retryable {
		t.Fatalf("expected rate limit to be retryable, err=%v", err)
	}
	if delay < time.Minute {
		t.Fatalf("expected retry delay >= 1 minute, got %s", delay)
	}
}

func TestClient_PostJSON(t *testing.T) {
	var auth string
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := client.PostJSON(context.Background(), server.URL, "callback-token", TaskEvent{
		Event:     EventDone,
		TaskID:    "task-1",
		SiteName:  "dc1",
		UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("post json failed: %v", err)
	}
	if auth != "Bearer callback-token" {
		t.Fatalf("unexpected auth header: %s", auth)
	}
	if !strings.Contains(body, "\"event\":\"done\"") {
		t.Fatalf("unexpected body: %s", body)
	}
}
