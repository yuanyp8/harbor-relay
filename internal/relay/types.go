package relay

import (
	"time"

	relayv1 "github.com/yuanyp8/harbor-relay/gen/proto/relay/v1"
	callbackmod "github.com/yuanyp8/harbor-relay/internal/callback"
)

// Task is the persisted sync unit created from one Harbor event.
// Each task means "one digest of one repository should be synchronized to one site".
type Task struct {
	ID                   string               `json:"id"`
	EventID              string               `json:"event_id"`
	Channel              string               `json:"channel"`
	SiteName             string               `json:"site_name"`
	SourceRegistry       string               `json:"source_registry"`
	Repository           string               `json:"repository"`
	Digest               string               `json:"digest"`
	Tags                 []string             `json:"tags"`
	SourcePullRef        string               `json:"source_pull_ref,omitempty"`
	SourceRefs           []string             `json:"source_refs,omitempty"`
	TargetRegistry       string               `json:"target_registry"`
	TargetRepository     string               `json:"target_repository"`
	CallbackEnabled      bool                 `json:"callback_enabled"`
	CallbackURL          string               `json:"callback_url,omitempty"`
	CallbackToken        string               `json:"callback_token,omitempty"`
	CallbackStatus       string               `json:"callback_status,omitempty"`
	CallbackMessage      string               `json:"callback_message,omitempty"`
	CallbackUpdatedAt    time.Time            `json:"callback_updated_at,omitempty"`
	Metadata             map[string]string    `json:"metadata,omitempty"`
	Status               relayv1.TaskStatus   `json:"status"`
	AssignedAgentID      string               `json:"assigned_agent_id,omitempty"`
	Message              string               `json:"message,omitempty"`
	TargetRefs           []string             `json:"target_refs,omitempty"`
	TargetRefDescriptors []string             `json:"target_ref_descriptors,omitempty"`
	NotifiedEvents       map[string]time.Time `json:"notified_events,omitempty"`
	Attempts             int                  `json:"attempts"`
	CreatedAt            time.Time            `json:"created_at"`
	UpdatedAt            time.Time            `json:"updated_at"`
}

type NotificationJobStatus string

const (
	NotificationJobStatusPending  NotificationJobStatus = "pending"
	NotificationJobStatusRetrying NotificationJobStatus = "retrying"
	NotificationJobStatusFailed   NotificationJobStatus = "failed"
)

// NotificationJob is a persisted notification queue item.
// One task event may fan out into multiple jobs because different robots may
// subscribe to different events or different audience groups.
type NotificationJob struct {
	ID            string                `json:"id"`
	TaskID        string                `json:"task_id"`
	SiteName      string                `json:"site_name"`
	ChannelName   string                `json:"channel_name"`
	ChannelKey    string                `json:"channel_key"`
	ReceiptKey    string                `json:"receipt_key"`
	Event         string                `json:"event"`
	Status        NotificationJobStatus `json:"status"`
	Payload       callbackmod.TaskEvent `json:"payload"`
	Attempts      int                   `json:"attempts"`
	NextAttemptAt time.Time             `json:"next_attempt_at"`
	LastError     string                `json:"last_error,omitempty"`
	CreatedAt     time.Time             `json:"created_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
}

// NotificationChannelState tracks the pacing window of one outbound robot.
// Each robot has its own cooldown so multiple robots can work independently.
type NotificationChannelState struct {
	ChannelKey    string    `json:"channel_key"`
	SiteName      string    `json:"site_name"`
	ChannelName   string    `json:"channel_name"`
	LastSentAt    time.Time `json:"last_sent_at,omitempty"`
	NextAllowedAt time.Time `json:"next_allowed_at,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Agent describes one connected remote consumer.
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

// State is the full JSON persistence model stored on disk.
type State struct {
	Tasks                    map[string]*Task                     `json:"tasks"`
	Agents                   map[string]*Agent                    `json:"agents"`
	NotificationJobs         map[string]*NotificationJob          `json:"notification_jobs,omitempty"`
	NotificationChannelState map[string]*NotificationChannelState `json:"notification_channel_state,omitempty"`
}
