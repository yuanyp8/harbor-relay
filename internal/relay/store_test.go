package relay

import (
	"testing"
	"time"

	relayv1 "github.com/yuanyp8/harbor-relay/gen/proto/relay/v1"
)

func TestMarkAgentOffline_RequeuesTask(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}
	now := time.Now()

	if err := store.UpsertAgent(&Agent{
		AgentID:       "agent-1",
		SiteName:      "dc1",
		Channels:      []string{"db-core"},
		CurrentTaskID: "task-1",
		ConnectedAt:   now,
		LastSeenAt:    now,
	}); err != nil {
		t.Fatalf("upsert agent failed: %v", err)
	}
	if err := store.AddTasks([]*Task{
		{
			ID:              "task-1",
			EventID:         "event-1",
			Channel:         "db-core",
			SiteName:        "dc1",
			Status:          relayv1.TaskStatus_TASK_STATUS_ASSIGNED,
			AssignedAgentID: "agent-1",
			CreatedAt:       now,
			UpdatedAt:       now,
		},
	}); err != nil {
		t.Fatalf("add tasks failed: %v", err)
	}

	if err := store.MarkAgentOffline("agent-1"); err != nil {
		t.Fatalf("mark offline failed: %v", err)
	}

	task, ok := store.GetTask("task-1")
	if !ok {
		t.Fatal("expected task to exist")
	}
	if task.Status != relayv1.TaskStatus_TASK_STATUS_PENDING {
		t.Fatalf("expected task to be requeued, got %v", task.Status)
	}
	if task.AssignedAgentID != "" {
		t.Fatalf("expected assigned agent to be cleared, got %s", task.AssignedAgentID)
	}
}

func TestUpdateTaskProgress_FinalClearsCurrentTask(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}
	now := time.Now()
	if err := store.UpsertAgent(&Agent{
		AgentID:       "agent-1",
		SiteName:      "dc1",
		Channels:      []string{"db-core"},
		CurrentTaskID: "task-1",
		ConnectedAt:   now,
		LastSeenAt:    now,
	}); err != nil {
		t.Fatalf("upsert agent failed: %v", err)
	}
	if err := store.AddTasks([]*Task{
		{
			ID:              "task-1",
			EventID:         "event-1",
			Channel:         "db-core",
			SiteName:        "dc1",
			Status:          relayv1.TaskStatus_TASK_STATUS_ASSIGNED,
			AssignedAgentID: "agent-1",
			CreatedAt:       now,
			UpdatedAt:       now,
		},
	}); err != nil {
		t.Fatalf("add tasks failed: %v", err)
	}

	task, err := store.UpdateTaskProgress("agent-1", "task-1", relayv1.TaskStatus_TASK_STATUS_DONE, "ok", []string{"sealos.hub:5000/kube4/mysql:8.0.45"})
	if err != nil {
		t.Fatalf("update task progress failed: %v", err)
	}
	if task.Status != relayv1.TaskStatus_TASK_STATUS_DONE {
		t.Fatalf("unexpected task status: %v", task.Status)
	}

	agents := store.ListAgents()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].CurrentTaskID != "" {
		t.Fatalf("expected current task to be cleared, got %s", agents[0].CurrentTaskID)
	}
}
