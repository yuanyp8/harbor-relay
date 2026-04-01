package relay

import (
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
	callbackmod "github.com/yuanyp8/harbor-relay/internal/callback"
	"github.com/yuanyp8/harbor-relay/internal/config"
)

// Service 是 relay 的核心调度层。
// 它负责把 Harbor 的 webhook 事件转成内部任务，再交给 gRPC 长连接上的远端 agent 消费。
type Service struct {
	cfg            config.RelayConfig
	store          *Store
	logger         *slog.Logger
	callbackClient *callbackmod.Client
	notifier       *callbackmod.Notifier
	targets        map[string]config.TargetConfig
	webhooks       map[string]config.WebhookConfig
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
		cfg:            cfg,
		store:          store,
		logger:         logger,
		callbackClient: callbackmod.NewClient(logger),
		notifier:       callbackmod.NewNotifier(cfg.Targets, logger),
		targets:        targets,
		webhooks:       webhooks,
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
		s.logger.Warn("webhook request path not configured",
			"path", r.URL.Path,
			"method", r.Method,
			"remote_addr", r.RemoteAddr,
		)
		http.NotFound(w, r)
		return
	}
	s.logger.Info("webhook request received",
		"webhook_name", webhookCfg.Name,
		"path", r.URL.Path,
		"method", r.Method,
		"remote_addr", r.RemoteAddr,
		"user_agent", r.UserAgent(),
	)
	if r.Method != http.MethodPost {
		s.logger.Warn("webhook rejected because method is not POST",
			"webhook_name", webhookCfg.Name,
			"method", r.Method,
		)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if webhookCfg.Authorization != "" && r.Header.Get("Authorization") != webhookCfg.Authorization {
		s.logger.Warn("webhook authorization failed",
			"webhook_name", webhookCfg.Name,
			"path", r.URL.Path,
		)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		s.logger.Error("failed to read webhook body",
			"webhook_name", webhookCfg.Name,
			"err", err,
		)
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	eventID := digestForBody(body)
	s.logger.Info("webhook body accepted",
		"webhook_name", webhookCfg.Name,
		"event_id", eventID,
		"body_bytes", len(body),
		"body", formatWebhookBodyForLog(body),
	)
	if s.store.EventExists(eventID) {
		s.logger.Info("webhook ignored because event already exists",
			"webhook_name", webhookCfg.Name,
			"event_id", eventID,
		)
		writeJSON(w, http.StatusAccepted, map[string]any{
			"status":   "duplicate",
			"event_id": eventID,
		})
		return
	}

	var payload harborWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		s.logger.Warn("webhook payload is not valid json",
			"webhook_name", webhookCfg.Name,
			"event_id", eventID,
			"err", err,
		)
		http.Error(w, "invalid json payload", http.StatusBadRequest)
		return
	}

	eventData := payload.EventData
	if len(eventData.Resources) == 0 && len(payload.Data.Resources) > 0 {
		eventData = payload.Data
	}
	if !isPushEvent(payload.Type) {
		s.logger.Info("webhook ignored because event type is unsupported",
			"webhook_name", webhookCfg.Name,
			"event_id", eventID,
			"event_type", payload.Type,
		)
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
		s.logger.Warn("webhook payload missing repository name",
			"webhook_name", webhookCfg.Name,
			"event_id", eventID,
		)
		http.Error(w, "repository name missing", http.StatusBadRequest)
		return
	}
	s.logger.Info("webhook repository parsed",
		"webhook_name", webhookCfg.Name,
		"event_id", eventID,
		"event_type", payload.Type,
		"repository", repoName,
		"resource_count", len(eventData.Resources),
	)

	grouped := groupResourcesByDigest(eventData.Resources)
	s.logger.Info("webhook resources grouped by digest",
		"webhook_name", webhookCfg.Name,
		"event_id", eventID,
		"grouped_resources", grouped,
	)
	if len(grouped) == 0 {
		s.logger.Info("webhook ignored because no tagged resources were found",
			"webhook_name", webhookCfg.Name,
			"event_id", eventID,
			"repository", repoName,
		)
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
		s.logger.Info("webhook produced no tasks after route evaluation",
			"webhook_name", webhookCfg.Name,
			"event_id", eventID,
			"repository", repoName,
		)
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
	for _, task := range tasks {
		if err := s.notifyTaskEvent(r.Context(), task, callbackmod.EventQueued); err != nil {
			s.logger.Error("queued notification failed",
				"task_id", task.ID,
				"site_name", task.SiteName,
				"err", err,
			)
		}
	}
	s.logger.Info("webhook queued tasks successfully",
		"webhook_name", webhookCfg.Name,
		"event_id", eventID,
		"repository", repoName,
		"task_count", len(tasks),
		"target_sites", collectSites(tasks),
	)

	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":       "queued",
		"event_id":     eventID,
		"task_count":   len(tasks),
		"repository":   repoName,
		"target_sites": collectSites(tasks),
	})
}

func (s *Service) TriggerCallback(ctx context.Context, task *Task) error {
	if !task.CallbackEnabled || task.CallbackURL == "" {
		return nil
	}

	return s.callbackClient.PostJSON(ctx, task.CallbackURL, task.CallbackToken, s.toTaskEvent(task, callbackmod.EventDone))
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
			s.logger.Info("route skipped because it is disabled",
				"route_name", route.Name,
				"repository", repoName,
				"webhook_name", webhookName,
			)
			continue
		}
		if !route.AllowsWebhook(webhookName) {
			s.logger.Info("route skipped because webhook is not allowed",
				"route_name", route.Name,
				"repository", repoName,
				"webhook_name", webhookName,
			)
			continue
		}
		if !matchesRepository(route.RepositoryPatterns, repoName) {
			s.logger.Info("route skipped because repository did not match",
				"route_name", route.Name,
				"repository", repoName,
				"webhook_name", webhookName,
				"patterns", route.RepositoryPatterns,
			)
			continue
		}

		channel := route.Channel
		if channel == "" {
			channel = route.Name
		}
		s.logger.Info("route matched repository",
			"route_name", route.Name,
			"channel", channel,
			"repository", repoName,
			"target_sites", route.TargetSites,
			"webhook_name", webhookName,
		)

		// 一次 Harbor 事件可能要下发给多个远端站点。
		// route 决定逻辑 channel，target_sites 决定哪些站点订阅这个 channel。
		for _, siteName := range route.TargetSites {
			target, ok := s.targets[siteName]
			if !ok || !target.IsEnabled() {
				s.logger.Warn("target site skipped because it is missing or disabled",
					"route_name", route.Name,
					"site_name", siteName,
					"repository", repoName,
				)
				continue
			}
			targetRepo := buildTargetRepository(target.RepositoryPrefix, target.TargetProject, repoName)
			for digest, tags := range grouped {
				sourcePullRef, sourceRefs := buildSourceReferences(sourceRegistry, repoName, digest, tags)
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
					SourcePullRef:    sourcePullRef,
					SourceRefs:       sourceRefs,
					TargetRegistry:   target.TargetRegistry,
					TargetRepository: targetRepo,
					CallbackEnabled:  target.IsCallbackEnabled(),
					CallbackURL:      target.CallbackURL,
					CallbackToken:    target.CallbackToken,
					Status:           relayv1.TaskStatus_TASK_STATUS_PENDING,
					Metadata: map[string]string{
						"harbor_event_type": eventType,
						"operator":          operator,
						"route_name":        route.Name,
						"route_channel":     channel,
						"webhook_name":      webhookName,
						"target_project":    target.TargetProject,
					},
					CreatedAt: now,
					UpdatedAt: now,
				})
				s.logger.Info("task created from route",
					"task_id", tasks[len(tasks)-1].ID,
					"route_name", route.Name,
					"channel", channel,
					"site_name", siteName,
					"repository", repoName,
					"digest", digest,
					"tags", tags,
					"source_pull_ref", sourcePullRef,
					"target_repository", targetRepo,
				)
			}
		}
	}
	return tasks
}

func (s *Service) toTaskEvent(task *Task, event callbackmod.Event) callbackmod.TaskEvent {
	return callbackmod.TaskEvent{
		Event:                event,
		TaskID:               task.ID,
		EventID:              task.EventID,
		SiteName:             task.SiteName,
		Channel:              task.Channel,
		Status:               task.Status.String(),
		SourceRegistry:       task.SourceRegistry,
		Repository:           task.Repository,
		Digest:               task.Digest,
		Tags:                 append([]string(nil), task.Tags...),
		SourcePullRef:        task.SourcePullRef,
		SourceRefs:           append([]string(nil), task.SourceRefs...),
		TargetRegistry:       task.TargetRegistry,
		TargetRepository:     task.TargetRepository,
		TargetRefs:           append([]string(nil), task.TargetRefs...),
		TargetRefDescriptors: append([]string(nil), task.TargetRefDescriptors...),
		Message:              task.Message,
		CallbackStatus:       task.CallbackStatus,
		CallbackMessage:      task.CallbackMessage,
		Metadata:             cloneMap(task.Metadata),
		UpdatedAt:            task.UpdatedAt,
	}
}

func (s *Service) notifyTaskEvent(ctx context.Context, task *Task, event callbackmod.Event) error {
	_ = ctx
	if s.notifier == nil {
		return nil
	}
	payload := s.toTaskEvent(task, event)
	channels := s.notifier.MatchingChannels(task.SiteName, event)
	if len(channels) == 0 {
		s.logger.Debug("notification skipped because no channel matched",
			"task_id", task.ID,
			"site_name", task.SiteName,
			"event", string(event),
		)
		return nil
	}

	now := time.Now()
	jobs := make([]*NotificationJob, 0, len(channels))
	for _, channel := range channels {
		receiptKey := buildNotificationReceiptKey(event, channel.Name)
		if s.store.HasTaskEventNotification(task.ID, receiptKey) {
			s.logger.Debug("notification skipped because receipt already exists",
				"task_id", task.ID,
				"site_name", task.SiteName,
				"event", string(event),
				"channel_name", channel.Name,
			)
			continue
		}
		if s.store.HasPendingNotificationJob(task.ID, receiptKey) {
			s.logger.Debug("notification skipped because job is already queued",
				"task_id", task.ID,
				"site_name", task.SiteName,
				"event", string(event),
				"channel_name", channel.Name,
			)
			continue
		}

		jobs = append(jobs, &NotificationJob{
			ID:            buildNotificationJobID(task.ID, receiptKey),
			TaskID:        task.ID,
			SiteName:      task.SiteName,
			ChannelName:   channel.Name,
			ChannelKey:    buildNotificationChannelKey(task.SiteName, channel.Name),
			ReceiptKey:    receiptKey,
			Event:         string(event),
			Status:        NotificationJobStatusPending,
			Payload:       payload,
			NextAttemptAt: now,
			CreatedAt:     now,
			UpdatedAt:     now,
		})
	}
	if len(jobs) == 0 {
		return nil
	}

	if err := s.store.EnqueueNotificationJobs(jobs); err != nil {
		return err
	}
	s.logger.Info("notification jobs queued",
		"task_id", task.ID,
		"site_name", task.SiteName,
		"event", string(event),
		"job_count", len(jobs),
	)
	return nil
}

func (s *Service) buildTasksFromTargets(eventID, eventType, operator, repoName string, grouped map[string][]string, sourceRegistry, webhookName string) []*Task {
	tasks := make([]*Task, 0)
	now := time.Now()
	seen := map[string]struct{}{}

	for _, target := range s.cfg.Targets {
		if !target.IsEnabled() {
			s.logger.Info("target skipped because it is disabled",
				"site_name", target.SiteName,
				"repository", repoName,
			)
			continue
		}
		if !matchesRepository(target.RepositoryPatterns, repoName) {
			s.logger.Info("target skipped because repository did not match",
				"site_name", target.SiteName,
				"repository", repoName,
				"patterns", target.RepositoryPatterns,
			)
			continue
		}
		targetRepo := buildTargetRepository(target.RepositoryPrefix, target.TargetProject, repoName)
		for digest, tags := range grouped {
			sourcePullRef, sourceRefs := buildSourceReferences(sourceRegistry, repoName, digest, tags)
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
				SourcePullRef:    sourcePullRef,
				SourceRefs:       sourceRefs,
				TargetRegistry:   target.TargetRegistry,
				TargetRepository: targetRepo,
				CallbackEnabled:  target.IsCallbackEnabled(),
				CallbackURL:      target.CallbackURL,
				CallbackToken:    target.CallbackToken,
				Status:           relayv1.TaskStatus_TASK_STATUS_PENDING,
				Metadata: map[string]string{
					"harbor_event_type": eventType,
					"operator":          operator,
					"webhook_name":      webhookName,
					"target_project":    target.TargetProject,
				},
				CreatedAt: now,
				UpdatedAt: now,
			})
			s.logger.Info("task created from target fallback",
				"task_id", tasks[len(tasks)-1].ID,
				"site_name", target.SiteName,
				"repository", repoName,
				"digest", digest,
				"tags", tags,
				"source_pull_ref", sourcePullRef,
				"target_repository", targetRepo,
			)
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

func buildTargetRepository(prefix, targetProject, repository string) string {
	if targetProject != "" {
		repository = rewriteRepositoryProject(repository, targetProject)
	}
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return repository
	}
	return path.Join(prefix, repository)
}

func rewriteRepositoryProject(repository, targetProject string) string {
	targetProject = strings.Trim(targetProject, "/")
	if targetProject == "" {
		return repository
	}
	parts := strings.SplitN(strings.Trim(repository, "/"), "/", 2)
	if len(parts) == 1 {
		return targetProject
	}
	return path.Join(targetProject, parts[1])
}

func buildSourceReferences(sourceRegistry, repository, digest string, tags []string) (string, []string) {
	sourcePullRef := fmt.Sprintf("%s/%s@%s", sourceRegistry, repository, digest)
	refs := make([]string, 0, len(tags))
	for _, tag := range tags {
		refs = append(refs, fmt.Sprintf("%s/%s:%s@%s", sourceRegistry, repository, tag, digest))
	}
	return sourcePullRef, refs
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

func formatWebhookBodyForLog(body []byte) string {
	const maxLogBodyBytes = 64 << 10
	if len(body) <= maxLogBodyBytes {
		return string(body)
	}
	return string(body[:maxLogBodyBytes]) + "...<truncated>"
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
