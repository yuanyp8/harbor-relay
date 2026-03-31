package relay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	relayv1 "github.com/yuanyp8/harbor-relay/gen/proto/relay/v1"
	"github.com/yuanyp8/harbor-relay/internal/config"
)

// Service 是 relay 的核心调度层。
// 它负责把 Harbor 的 webhook 事件转成内部任务，再交给 gRPC 长连接上的远端 agent 消费。
type Service struct {
	cfg        config.RelayConfig
	store      *Store
	logger     *slog.Logger
	httpClient *http.Client
	targets    map[string]config.TargetConfig
	webhooks   map[string]config.WebhookConfig
}

// NewService builds the runtime view of relay configuration.
// Targets represent remote DC destinations, while webhooks and routes define
// how Harbor push events are accepted and turned into queued tasks.
func NewService(cfg config.RelayConfig, store *Store, logger *slog.Logger) *Service {
	targets := make(map[string]config.TargetConfig, len(cfg.Targets))
	for _, target := range cfg.Targets {
		targets[target.SiteName] = target
	}
	webhooks := make(map[string]config.WebhookConfig, len(cfg.Webhooks))
	for _, webhook := range cfg.Webhooks {
		webhooks[webhook.Path] = webhook
	}
	return &Service{
		cfg:    cfg,
		store:  store,
		logger: logger,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		targets:  targets,
		webhooks: webhooks,
	}
}

type harborWebhookPayload struct {
	Type      string          `json:"type"`
	EventData harborEventData `json:"event_data"`
	Data      harborEventData `json:"data"`
	OccurAt   int64           `json:"occur_at"`
	Operator  string          `json:"operator"`
}

type harborEventData struct {
	Repository harborRepository `json:"repository"`
	Resources  []harborResource `json:"resources"`
}

type harborRepository struct {
	Name         string `json:"name"`
	RepoFullName string `json:"repo_full_name"`
	FullName     string `json:"full_name"`
}

type harborResource struct {
	Digest      string `json:"digest"`
	Tag         string `json:"tag"`
	ResourceURL string `json:"resource_url"`
}

// HandleWebhook 是 Harbor HTTP 事件进入 relay 的主入口。
// 它依次完成 4 件事：
// 1. 校验 webhook 配置和鉴权头
// 2. 解析 Harbor payload，抽取 repository / digest / tags
// 3. 按 route 和 target 展开成内部任务
// 4. 落到 store，等待远端 agent 通过 gRPC 拉取
func (s *Service) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	webhookCfg, ok := s.webhooks[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if webhookCfg.Authorization != "" && r.Header.Get("Authorization") != webhookCfg.Authorization {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	eventID := digestForBody(body)
	if s.store.EventExists(eventID) {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"status":   "duplicate",
			"event_id": eventID,
		})
		return
	}

	var payload harborWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid json payload", http.StatusBadRequest)
		return
	}

	eventData := payload.EventData
	if len(eventData.Resources) == 0 && len(payload.Data.Resources) > 0 {
		eventData = payload.Data
	}
	if !isPushEvent(payload.Type) {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"status":   "ignored",
			"event_id": eventID,
			"reason":   "unsupported event type",
			"type":     payload.Type,
		})
		return
	}

	repoName := eventData.Repository.RepoFullName
	if repoName == "" {
		repoName = eventData.Repository.FullName
	}
	if repoName == "" {
		http.Error(w, "repository name missing", http.StatusBadRequest)
		return
	}

	grouped := groupResourcesByDigest(eventData.Resources)
	if len(grouped) == 0 {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"status":   "ignored",
			"event_id": eventID,
			"reason":   "no tagged resources in payload",
		})
		return
	}

	sourceRegistry := webhookCfg.SourceRegistry
	if sourceRegistry == "" {
		sourceRegistry = s.cfg.SourceRegistry
	}
	tasks := s.buildTasks(eventID, payload.Type, payload.Operator, repoName, grouped, sourceRegistry, webhookCfg.Name)

	if len(tasks) == 0 {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"status":   "ignored",
			"event_id": eventID,
			"reason":   "no targets matched",
		})
		return
	}

	if err := s.store.AddTasks(tasks); err != nil {
		s.logger.Error("store add tasks failed", "err", err)
		http.Error(w, "failed to persist tasks", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":       "queued",
		"event_id":     eventID,
		"task_count":   len(tasks),
		"repository":   repoName,
		"target_sites": collectSites(tasks),
	})
}

func (s *Service) TriggerCallback(ctx context.Context, task *Task) error {
	if task.CallbackURL == "" {
		return nil
	}

	payload := map[string]any{
		"task_id":           task.ID,
		"event_id":          task.EventID,
		"site_name":         task.SiteName,
		"status":            task.Status.String(),
		"source_registry":   task.SourceRegistry,
		"repository":        task.Repository,
		"digest":            task.Digest,
		"tags":              task.Tags,
		"target_registry":   task.TargetRegistry,
		"target_repository": task.TargetRepository,
		"target_refs":       task.TargetRefs,
		"message":           task.Message,
		"updated_at":        task.UpdatedAt,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, task.CallbackURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if task.CallbackToken != "" {
		req.Header.Set("Authorization", "Bearer "+task.CallbackToken)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("callback status %d", resp.StatusCode)
	}
	return nil
}

// buildTasks 把一次 Harbor push 事件展开成一个或多个投递任务。
// 优先走 routes 模式，这样可以先映射 channel，再映射 target sites。
// 如果没配 routes，则退回旧的 target 直连模式，兼容已有配置。
func (s *Service) buildTasks(eventID, eventType, operator, repoName string, grouped map[string][]string, sourceRegistry, webhookName string) []*Task {
	if len(s.cfg.Routes) > 0 {
		return s.buildTasksFromRoutes(eventID, eventType, operator, repoName, grouped, sourceRegistry, webhookName)
	}
	return s.buildTasksFromTargets(eventID, eventType, operator, repoName, grouped, sourceRegistry, webhookName)
}

func (s *Service) buildTasksFromRoutes(eventID, eventType, operator, repoName string, grouped map[string][]string, sourceRegistry, webhookName string) []*Task {
	tasks := make([]*Task, 0)
	now := time.Now()
	seen := map[string]struct{}{}

	for _, route := range s.cfg.Routes {
		if !route.IsEnabled() {
			continue
		}
		if !matchesRepository(route.RepositoryPatterns, repoName) {
			continue
		}

		channel := route.Channel
		if channel == "" {
			channel = route.Name
		}

		// 一次 Harbor 事件可能要下发给多个远端站点。
		// route 决定逻辑 channel，target_sites 决定哪些站点订阅这个 channel。
		for _, siteName := range route.TargetSites {
			target, ok := s.targets[siteName]
			if !ok || !target.IsEnabled() {
				continue
			}
			targetRepo := buildTargetRepository(target.RepositoryPrefix, repoName)
			for digest, tags := range grouped {
				key := logicalTaskKey(siteName, channel, repoName, digest)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				tasks = append(tasks, &Task{
					ID:               buildTaskID(siteName, channel, repoName, digest),
					EventID:          eventID,
					Channel:          channel,
					SiteName:         siteName,
					SourceRegistry:   sourceRegistry,
					Repository:       repoName,
					Digest:           digest,
					Tags:             tags,
					TargetRegistry:   target.TargetRegistry,
					TargetRepository: targetRepo,
					CallbackURL:      target.CallbackURL,
					CallbackToken:    target.CallbackToken,
					Status:           relayv1.TaskStatus_TASK_STATUS_PENDING,
					Metadata: map[string]string{
						"harbor_event_type": eventType,
						"operator":          operator,
						"route_name":        route.Name,
						"route_channel":     channel,
						"webhook_name":      webhookName,
					},
					CreatedAt: now,
					UpdatedAt: now,
				})
			}
		}
	}
	return tasks
}

func (s *Service) buildTasksFromTargets(eventID, eventType, operator, repoName string, grouped map[string][]string, sourceRegistry, webhookName string) []*Task {
	tasks := make([]*Task, 0)
	now := time.Now()
	seen := map[string]struct{}{}

	for _, target := range s.cfg.Targets {
		if !target.IsEnabled() {
			continue
		}
		if !matchesRepository(target.RepositoryPatterns, repoName) {
			continue
		}
		targetRepo := buildTargetRepository(target.RepositoryPrefix, repoName)
		for digest, tags := range grouped {
			key := logicalTaskKey(target.SiteName, "default", repoName, digest)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			tasks = append(tasks, &Task{
				ID:               buildTaskID(target.SiteName, "default", repoName, digest),
				EventID:          eventID,
				Channel:          "default",
				SiteName:         target.SiteName,
				SourceRegistry:   sourceRegistry,
				Repository:       repoName,
				Digest:           digest,
				Tags:             tags,
				TargetRegistry:   target.TargetRegistry,
				TargetRepository: targetRepo,
				CallbackURL:      target.CallbackURL,
				CallbackToken:    target.CallbackToken,
				Status:           relayv1.TaskStatus_TASK_STATUS_PENDING,
				Metadata: map[string]string{
					"harbor_event_type": eventType,
					"operator":          operator,
					"webhook_name":      webhookName,
				},
				CreatedAt: now,
				UpdatedAt: now,
			})
		}
	}
	return tasks
}

func isPushEvent(eventType string) bool {
	switch strings.ToUpper(eventType) {
	case "PUSH_ARTIFACT", "HARBOR.ARTIFACT.PUSHED":
		return true
	default:
		return false
	}
}

func groupResourcesByDigest(resources []harborResource) map[string][]string {
	grouped := map[string][]string{}
	for _, resource := range resources {
		if resource.Digest == "" {
			continue
		}
		if !isUsableTag(resource.Tag) {
			continue
		}
		grouped[resource.Digest] = appendUnique(grouped[resource.Digest], resource.Tag)
	}
	return grouped
}

// matchesRepository 用 doublestar 做仓库匹配。
// 例如：
// - kube4/mysql
// - kube4/redis*
// - cmict/**
func matchesRepository(patterns []string, repository string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		ok, err := doublestar.Match(pattern, repository)
		if err == nil && ok {
			return true
		}
	}
	return false
}

func buildTargetRepository(prefix, repository string) string {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return repository
	}
	return path.Join(prefix, repository)
}

func buildTaskID(siteName, channel, repository, digest string) string {
	sum := sha256.Sum256([]byte(siteName + "|" + channel + "|" + repository + "|" + digest + "|" + time.Now().UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:12])
}

func logicalTaskKey(siteName, channel, repository, digest string) string {
	return siteName + "|" + channel + "|" + repository + "|" + digest
}

func digestForBody(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:16])
}

func appendUnique(items []string, item string) []string {
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	return append(items, item)
}

func isUsableTag(tag string) bool {
	if tag == "" {
		return false
	}
	return !strings.HasPrefix(tag, "sha256:")
}

func collectSites(tasks []*Task) []string {
	sites := make([]string, 0, len(tasks))
	seen := map[string]struct{}{}
	for _, task := range tasks {
		if _, ok := seen[task.SiteName]; ok {
			continue
		}
		seen[task.SiteName] = struct{}{}
		sites = append(sites, task.SiteName)
	}
	return sites
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
