package relay

import (
	"time"

	relayv1 "github.com/yuanyp8/harbor-relay/gen/proto/relay/v1"
)

// Task 是 relay 落盘的任务实体。
// 它代表“某个 Harbor 事件中的某个 digest，要同步到某个站点”的最小工作单元。
type Task struct {
	ID               string             `json:"id"`
	EventID          string             `json:"event_id"`
	Channel          string             `json:"channel"`
	SiteName         string             `json:"site_name"`
	SourceRegistry   string             `json:"source_registry"`
	Repository       string             `json:"repository"`
	Digest           string             `json:"digest"`
	Tags             []string           `json:"tags"`
	TargetRegistry   string             `json:"target_registry"`
	TargetRepository string             `json:"target_repository"`
	CallbackURL      string             `json:"callback_url,omitempty"`
	CallbackToken    string             `json:"callback_token,omitempty"`
	Metadata         map[string]string  `json:"metadata,omitempty"`
	Status           relayv1.TaskStatus `json:"status"`
	AssignedAgentID  string             `json:"assigned_agent_id,omitempty"`
	Message          string             `json:"message,omitempty"`
	TargetRefs       []string           `json:"target_refs,omitempty"`
	Attempts         int                `json:"attempts"`
	CreatedAt        time.Time          `json:"created_at"`
	UpdatedAt        time.Time          `json:"updated_at"`
}

// Agent 表示当前连接到 relay 的远端消费节点状态。
type Agent struct {
	AgentID       string    `json:"agent_id"`
	SiteName      string    `json:"site_name"`
	Channels      []string  `json:"channels,omitempty"`
	Version       string    `json:"version,omitempty"`
	Capabilities  []string  `json:"capabilities,omitempty"`
	Online        bool      `json:"online"`
	CurrentTaskID string    `json:"current_task_id,omitempty"`
	LastSeenAt    time.Time `json:"last_seen_at"`
	ConnectedAt   time.Time `json:"connected_at"`
}

// State 是当前 JSON 状态文件的整体结构。
type State struct {
	Tasks  map[string]*Task  `json:"tasks"`
	Agents map[string]*Agent `json:"agents"`
}
