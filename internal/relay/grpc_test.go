package relay

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	relayv1 "github.com/yuanyp8/harbor-relay/gen/proto/relay/v1"
	"github.com/yuanyp8/harbor-relay/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestGRPCConnect_AssignsTaskAndAcceptsProgress(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}
	now := time.Now()
	if err := store.AddTasks([]*Task{
		{
			ID:               "task-1",
			EventID:          "event-1",
			Channel:          "db-core",
			SiteName:         "dc1",
			SourceRegistry:   "registry.example.com:9443",
			Repository:       "kube4/mysql",
			Digest:           "sha256:abc",
			Tags:             []string{"8.0.45"},
			TargetRegistry:   "sealos.hub:5000",
			TargetRepository: "kube4/mysql",
			Status:           relayv1.TaskStatus_TASK_STATUS_PENDING,
			CreatedAt:        now,
			UpdatedAt:        now,
		},
	}); err != nil {
		t.Fatalf("add tasks failed: %v", err)
	}

	service := NewService(config.RelayConfig{ServiceName: "harbor-relay"}, store, testLogger())
	server := grpc.NewServer()
	relayv1.RegisterRelayServiceServer(server, NewGRPCServer(service, testLogger()))

	listener := bufconn.Listen(1024 * 1024)
	defer listener.Close()
	go func() {
		_ = server.Serve(listener)
	}()
	defer server.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc dial failed: %v", err)
	}
	defer conn.Close()

	client := relayv1.NewRelayServiceClient(conn)
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("connect stream failed: %v", err)
	}

	err = stream.Send(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Hello{
			Hello: &relayv1.AgentHello{
				AgentId:  "agent-1",
				SiteName: "dc1",
				Channels: []string{"db-core"},
				Version:  "test",
			},
		},
	})
	if err != nil {
		t.Fatalf("send hello failed: %v", err)
	}

	msg1, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv ack failed: %v", err)
	}
	if msg1.GetAck() == nil {
		t.Fatalf("expected first message to be ack, got %#v", msg1.Payload)
	}

	msg2, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv task failed: %v", err)
	}
	task := msg2.GetTask()
	if task == nil {
		t.Fatalf("expected task assignment, got %#v", msg2.Payload)
	}
	if task.Channel != "db-core" {
		t.Fatalf("unexpected task channel: %s", task.Channel)
	}

	err = stream.Send(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Progress{
			Progress: &relayv1.TaskProgress{
				TaskId:               task.TaskId,
				Status:               relayv1.TaskStatus_TASK_STATUS_DONE,
				Message:              "ok",
				TargetRefs:           []string{"sealos.hub:5000/kube4/mysql:8.0.45"},
				TargetRefDescriptors: []string{"sealos.hub:5000/kube4/mysql:8.0.45@sha256:abc"},
			},
		},
	})
	if err != nil {
		t.Fatalf("send progress failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		storedTask, ok := store.GetTask("task-1")
		if !ok {
			t.Fatal("expected task to exist in store")
		}
		if storedTask.Status == relayv1.TaskStatus_TASK_STATUS_DONE {
			if len(storedTask.TargetRefDescriptors) != 1 {
				t.Fatalf("expected target ref descriptors to be stored, got %d", len(storedTask.TargetRefDescriptors))
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	storedTask, _ := store.GetTask("task-1")
	t.Fatalf("expected task status done, got %v", storedTask.Status)
}

func TestGRPCConnect_DoneProgressTriggersNotification(t *testing.T) {
	var notified bool
	robotServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		notified = true
		_, _ = io.WriteString(w, `{"success":true}`)
	}))
	defer robotServer.Close()

	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}
	now := time.Now()
	if err := store.AddTasks([]*Task{
		{
			ID:               "task-2",
			EventID:          "event-2",
			Channel:          "db-core",
			SiteName:         "dc1",
			SourceRegistry:   "registry.example.com:9443",
			Repository:       "kube4/mysql",
			Digest:           "sha256:def",
			Tags:             []string{"8.0.45"},
			TargetRegistry:   "sealos.hub:5000",
			TargetRepository: "kube4/mysql",
			Status:           relayv1.TaskStatus_TASK_STATUS_PENDING,
			CreatedAt:        now,
			UpdatedAt:        now,
		},
	}); err != nil {
		t.Fatalf("add tasks failed: %v", err)
	}

	service := NewService(config.RelayConfig{
		ServiceName: "harbor-relay",
		Targets: []config.TargetConfig{
			{
				Name:           "dc1",
				SiteName:       "dc1",
				TargetRegistry: "sealos.hub:5000",
				Notifications: []config.NotificationConfig{
					{
						Name:     "ops-group",
						Type:     "onemsg_robot",
						Endpoint: robotServer.URL,
						RobotKey: "replace-with-robot-key",
						Events:   []string{"done"},
					},
				},
			},
		},
	}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := grpc.NewServer()
	relayv1.RegisterRelayServiceServer(server, NewGRPCServer(service, testLogger()))

	listener := bufconn.Listen(1024 * 1024)
	defer listener.Close()
	go func() {
		_ = server.Serve(listener)
	}()
	defer server.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc dial failed: %v", err)
	}
	defer conn.Close()

	client := relayv1.NewRelayServiceClient(conn)
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("connect stream failed: %v", err)
	}

	if err := stream.Send(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Hello{
			Hello: &relayv1.AgentHello{
				AgentId:  "agent-done",
				SiteName: "dc1",
				Channels: []string{"db-core"},
				Version:  "test",
			},
		},
	}); err != nil {
		t.Fatalf("send hello failed: %v", err)
	}

	if _, err := stream.Recv(); err != nil {
		t.Fatalf("recv ack failed: %v", err)
	}
	taskMsg, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv task failed: %v", err)
	}
	task := taskMsg.GetTask()
	if task == nil {
		t.Fatalf("expected task assignment, got %#v", taskMsg.Payload)
	}

	if err := stream.Send(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Progress{
			Progress: &relayv1.TaskProgress{
				TaskId:               task.TaskId,
				Status:               relayv1.TaskStatus_TASK_STATUS_DONE,
				Message:              "ok",
				TargetRefs:           []string{"sealos.hub:5000/kube4/mysql:8.0.45"},
				TargetRefDescriptors: []string{"sealos.hub:5000/kube4/mysql:8.0.45@sha256:def"},
			},
		},
	}); err != nil {
		t.Fatalf("send progress failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := service.processNotificationQueueOnce(context.Background()); err != nil {
			t.Fatalf("process notification queue failed: %v", err)
		}
		if notified {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("expected done notification to be sent")
}

func TestGRPCConnect_CallbackFailureKeepsTaskDone(t *testing.T) {
	doneRobot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"success":true}`)
	}))
	defer doneRobot.Close()

	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}
	now := time.Now()
	if err := store.AddTasks([]*Task{
		{
			ID:               "task-callback-failure",
			EventID:          "event-callback-failure",
			Channel:          "db-core",
			SiteName:         "dc1",
			SourceRegistry:   "registry.example.com:9443",
			Repository:       "kube4/mysql",
			Digest:           "sha256:def",
			Tags:             []string{"8.0.45"},
			TargetRegistry:   "sealos.hub:5000",
			TargetRepository: "kube4/mysql",
			CallbackEnabled:  true,
			CallbackURL:      "https://127.0.0.1:1/callback",
			Status:           relayv1.TaskStatus_TASK_STATUS_PENDING,
			CreatedAt:        now,
			UpdatedAt:        now,
		},
	}); err != nil {
		t.Fatalf("add tasks failed: %v", err)
	}

	service := NewService(config.RelayConfig{
		ServiceName: "harbor-relay",
		Targets: []config.TargetConfig{
			{
				Name:           "dc1",
				SiteName:       "dc1",
				TargetRegistry: "sealos.hub:5000",
				Notifications: []config.NotificationConfig{
					{
						Name:     "done-group",
						Type:     "onemsg_robot",
						Endpoint: doneRobot.URL,
						RobotKey: "done-key",
						Events:   []string{"done"},
					},
				},
			},
		},
	}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := grpc.NewServer()
	relayv1.RegisterRelayServiceServer(server, NewGRPCServer(service, testLogger()))

	listener := bufconn.Listen(1024 * 1024)
	defer listener.Close()
	go func() {
		_ = server.Serve(listener)
	}()
	defer server.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc dial failed: %v", err)
	}
	defer conn.Close()

	client := relayv1.NewRelayServiceClient(conn)
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("connect stream failed: %v", err)
	}

	if err := stream.Send(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Hello{
			Hello: &relayv1.AgentHello{
				AgentId:  "agent-callback-failure",
				SiteName: "dc1",
				Channels: []string{"db-core"},
				Version:  "test",
			},
		},
	}); err != nil {
		t.Fatalf("send hello failed: %v", err)
	}

	if _, err := stream.Recv(); err != nil {
		t.Fatalf("recv ack failed: %v", err)
	}
	taskMsg, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv task failed: %v", err)
	}
	task := taskMsg.GetTask()
	if task == nil {
		t.Fatalf("expected task assignment, got %#v", taskMsg.Payload)
	}

	if err := stream.Send(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Progress{
			Progress: &relayv1.TaskProgress{
				TaskId:               task.TaskId,
				Status:               relayv1.TaskStatus_TASK_STATUS_DONE,
				Message:              "ok",
				TargetRefs:           []string{"sealos.hub:5000/kube4/mysql:8.0.45"},
				TargetRefDescriptors: []string{"sealos.hub:5000/kube4/mysql:8.0.45@sha256:def"},
			},
		},
	}); err != nil {
		t.Fatalf("send progress failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		storedTask, ok := store.GetTask("task-callback-failure")
		if !ok {
			t.Fatal("expected task to exist in store")
		}
		if storedTask.Status == relayv1.TaskStatus_TASK_STATUS_DONE && storedTask.CallbackStatus == "failed" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	storedTask, _ := store.GetTask("task-callback-failure")
	t.Fatalf("expected task status done with callback failure recorded, got status=%v callback_status=%s", storedTask.Status, storedTask.CallbackStatus)
}
