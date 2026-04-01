package relay

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	relayv1 "github.com/yuanyp8/harbor-relay/gen/proto/relay/v1"
)

// Store 是 relay 的轻量持久层。
// 当前版本故意用 JSON 文件，方便交付项目排障和人工查看。
type Store struct {
	path  string
	mu    sync.RWMutex
	state State
}

// NewStore 初始化本地状态存储。
func NewStore(path string) (*Store, error) {
	s := &Store{
		path: path,
		state: State{
			Tasks:  map[string]*Task{},
			Agents: map[string]*Agent{},
		},
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	if state.Tasks == nil {
		state.Tasks = map[string]*Task{}
	}
	if state.Agents == nil {
		state.Agents = map[string]*Agent{}
	}
	s.state = state
	return nil
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// EventExists 用于 webhook 去重。
// 当前去重粒度是“同一个 HTTP body 的哈希值已经出现过”。
func (s *Store) EventExists(eventID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, task := range s.state.Tasks {
		if task.EventID == eventID {
			return true
		}
	}
	return false
}

// AddTasks 批量写入新任务。
func (s *Store) AddTasks(tasks []*Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, task := range tasks {
		s.state.Tasks[task.ID] = task
	}
	return s.saveLocked()
}

// UpsertAgent 在 agent 建连时登记其在线状态和能力。
func (s *Store) UpsertAgent(agent *Agent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.state.Agents[agent.AgentID]
	if ok {
		existing.SiteName = agent.SiteName
		existing.Channels = slices.Clone(agent.Channels)
		existing.Version = agent.Version
		existing.Capabilities = slices.Clone(agent.Capabilities)
		existing.Online = true
		existing.LastSeenAt = time.Now()
		if existing.ConnectedAt.IsZero() {
			existing.ConnectedAt = time.Now()
		}
	} else {
		agent.Online = true
		agent.LastSeenAt = time.Now()
		agent.ConnectedAt = time.Now()
		agent.Channels = slices.Clone(agent.Channels)
		agent.Capabilities = slices.Clone(agent.Capabilities)
		s.state.Agents[agent.AgentID] = agent
	}
	return s.saveLocked()
}

// MarkHeartbeat 刷新 agent 最近活跃时间。
func (s *Store) MarkHeartbeat(agentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if agent, ok := s.state.Agents[agentID]; ok {
		agent.Online = true
		agent.LastSeenAt = time.Now()
		return s.saveLocked()
	}
	return nil
}

// MarkAgentOffline 在 gRPC 连接断开时把 agent 标记为离线，
// 并把它占着的任务重新放回待调度状态。
func (s *Store) MarkAgentOffline(agentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	agent, ok := s.state.Agents[agentID]
	if !ok {
		return nil
	}
	agent.Online = false
	agent.LastSeenAt = time.Now()
	if agent.CurrentTaskID != "" {
		if task, exists := s.state.Tasks[agent.CurrentTaskID]; exists {
			task.Status = relayv1.TaskStatus_TASK_STATUS_PENDING
			task.AssignedAgentID = ""
			task.Message = "agent disconnected, task requeued"
			task.UpdatedAt = time.Now()
		}
		agent.CurrentTaskID = ""
	}
	return s.saveLocked()
}

// AssignNextTask 负责按 site + channel 维度调度任务。
// 这是整个系统最关键的边界：
// 1. webhook 只负责产出任务
// 2. gRPC 只负责消费已经路由好的任务
func (s *Store) AssignNextTask(siteName string, channels []string, agentID string) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, ok := s.state.Agents[agentID]
	if !ok {
		return nil, nil
	}
	if agent.CurrentTaskID != "" {
		return nil, nil
	}

	var selected *Task
	for _, task := range s.state.Tasks {
		if task.SiteName != siteName {
			continue
		}
		if !channelAllowed(task.Channel, channels) {
			continue
		}
		if task.Status != relayv1.TaskStatus_TASK_STATUS_PENDING {
			continue
		}
		if selected == nil || task.CreatedAt.Before(selected.CreatedAt) {
			selected = task
		}
	}
	if selected == nil {
		return nil, nil
	}

	selected.Status = relayv1.TaskStatus_TASK_STATUS_ASSIGNED
	selected.AssignedAgentID = agentID
	selected.Attempts++
	selected.UpdatedAt = time.Now()
	agent.CurrentTaskID = selected.ID
	agent.Online = true
	agent.LastSeenAt = time.Now()

	if err := s.saveLocked(); err != nil {
		return nil, err
	}

	taskCopy := *selected
	taskCopy.Tags = slices.Clone(selected.Tags)
	taskCopy.SourceRefs = slices.Clone(selected.SourceRefs)
	taskCopy.TargetRefs = slices.Clone(selected.TargetRefs)
	taskCopy.TargetRefDescriptors = slices.Clone(selected.TargetRefDescriptors)
	taskCopy.Metadata = cloneMap(selected.Metadata)
	return &taskCopy, nil
}

// UpdateTaskProgress 持久化 agent 上报的最新任务状态。
// 如果任务已经进入最终状态，还会顺手释放 agent 的 current task 占位。
func (s *Store) UpdateTaskProgress(agentID, taskID string, status relayv1.TaskStatus, message string, targetRefs, targetRefDescriptors []string) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.state.Tasks[taskID]
	if !ok {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	task.Status = status
	task.Message = message
	task.TargetRefs = slices.Clone(targetRefs)
	task.TargetRefDescriptors = slices.Clone(targetRefDescriptors)
	task.UpdatedAt = time.Now()

	if agent, ok := s.state.Agents[agentID]; ok {
		agent.LastSeenAt = time.Now()
		agent.Online = true
		if isFinalStatus(status) && agent.CurrentTaskID == taskID {
			agent.CurrentTaskID = ""
		}
	}

	if err := s.saveLocked(); err != nil {
		return nil, err
	}

	taskCopy := *task
	taskCopy.Tags = slices.Clone(task.Tags)
	taskCopy.SourceRefs = slices.Clone(task.SourceRefs)
	taskCopy.TargetRefs = slices.Clone(task.TargetRefs)
	taskCopy.TargetRefDescriptors = slices.Clone(task.TargetRefDescriptors)
	taskCopy.Metadata = cloneMap(task.Metadata)
	return &taskCopy, nil
}

// ListTasks 返回按创建时间排序后的任务列表，便于 API 直接展示。
func (s *Store) ListTasks() []*Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Task, 0, len(s.state.Tasks))
	for _, task := range s.state.Tasks {
		taskCopy := *task
		taskCopy.Tags = slices.Clone(task.Tags)
		taskCopy.SourceRefs = slices.Clone(task.SourceRefs)
		taskCopy.TargetRefs = slices.Clone(task.TargetRefs)
		taskCopy.TargetRefDescriptors = slices.Clone(task.TargetRefDescriptors)
		taskCopy.Metadata = cloneMap(task.Metadata)
		result = append(result, &taskCopy)
	}
	slices.SortFunc(result, func(a, b *Task) int {
		switch {
		case a.CreatedAt.Before(b.CreatedAt):
			return -1
		case a.CreatedAt.After(b.CreatedAt):
			return 1
		default:
			return 0
		}
	})
	return result
}

// ListAgents 返回当前 agent 列表。
func (s *Store) ListAgents() []*Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Agent, 0, len(s.state.Agents))
	for _, agent := range s.state.Agents {
		agentCopy := *agent
		agentCopy.Channels = slices.Clone(agent.Channels)
		agentCopy.Capabilities = slices.Clone(agent.Capabilities)
		result = append(result, &agentCopy)
	}
	slices.SortFunc(result, func(a, b *Agent) int {
		switch {
		case a.AgentID < b.AgentID:
			return -1
		case a.AgentID > b.AgentID:
			return 1
		default:
			return 0
		}
	})
	return result
}

func (s *Store) GetTask(taskID string) (*Task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.state.Tasks[taskID]
	if !ok {
		return nil, false
	}
	taskCopy := *task
	taskCopy.Tags = slices.Clone(task.Tags)
	taskCopy.SourceRefs = slices.Clone(task.SourceRefs)
	taskCopy.TargetRefs = slices.Clone(task.TargetRefs)
	taskCopy.TargetRefDescriptors = slices.Clone(task.TargetRefDescriptors)
	taskCopy.Metadata = cloneMap(task.Metadata)
	return &taskCopy, true
}

func (s *Store) PendingTaskStats(siteName string, channels []string) (totalPending, sameSitePending, assignablePending int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, task := range s.state.Tasks {
		if task.Status != relayv1.TaskStatus_TASK_STATUS_PENDING {
			continue
		}
		totalPending++
		if task.SiteName != siteName {
			continue
		}
		sameSitePending++
		if channelAllowed(task.Channel, channels) {
			assignablePending++
		}
	}
	return totalPending, sameSitePending, assignablePending
}

func isFinalStatus(status relayv1.TaskStatus) bool {
	return status == relayv1.TaskStatus_TASK_STATUS_DONE ||
		status == relayv1.TaskStatus_TASK_STATUS_FAILED ||
		status == relayv1.TaskStatus_TASK_STATUS_CALLBACK_PENDING
}

func cloneMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// channelAllowed 用于判断某个任务 channel 是否被 agent 订阅。
func channelAllowed(taskChannel string, channels []string) bool {
	if len(channels) == 0 {
		return true
	}
	for _, channel := range channels {
		if channel == "*" || channel == taskChannel {
			return true
		}
	}
	return false
}
