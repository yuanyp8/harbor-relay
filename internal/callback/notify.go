package callback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yuanyp8/harbor-relay/internal/config"
)

const defaultOneMsgEndpoint = "https://office.example.com/onemsg-api/robot/pushToRobot"

type oneMsgResponse struct {
	Msg     string `json:"msg"`
	Code    int    `json:"code"`
	Success bool   `json:"success"`
}

// SendError describes a notification delivery failure and whether relay should retry.
type SendError struct {
	Message    string
	Retryable  bool
	RetryAfter time.Duration
}

func (e *SendError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Notifier holds notification channel definitions grouped by site name.
type Notifier struct {
	httpClient          *http.Client
	logger              *slog.Logger
	notificationsBySite map[string][]config.NotificationConfig
	notificationByKey   map[string]config.NotificationConfig
}

func NewNotifier(targets []config.TargetConfig, logger *slog.Logger) *Notifier {
	notificationsBySite := make(map[string][]config.NotificationConfig, len(targets))
	notificationByKey := make(map[string]config.NotificationConfig)
	for _, target := range targets {
		if !target.IsEnabled() || len(target.Notifications) == 0 {
			continue
		}
		cloned := append([]config.NotificationConfig(nil), target.Notifications...)
		notificationsBySite[target.SiteName] = cloned
		for _, channel := range cloned {
			notificationByKey[notificationLookupKey(target.SiteName, channel.Name)] = channel
		}
	}
	return &Notifier{
		httpClient:          &http.Client{Timeout: 15 * time.Second},
		logger:              logger,
		notificationsBySite: notificationsBySite,
		notificationByKey:   notificationByKey,
	}
}

func (n *Notifier) MatchingChannels(siteName string, event Event) []config.NotificationConfig {
	channels := n.notificationsBySite[siteName]
	if len(channels) == 0 {
		return nil
	}

	matched := make([]config.NotificationConfig, 0, len(channels))
	for _, channel := range channels {
		if !channel.IsEnabled() {
			continue
		}
		if !supportsEvent(channel.Events, event) {
			continue
		}
		matched = append(matched, channel)
	}
	return matched
}

func (n *Notifier) GetChannel(siteName, channelName string) (config.NotificationConfig, bool) {
	channel, ok := n.notificationByKey[notificationLookupKey(siteName, channelName)]
	return channel, ok
}

func (n *Notifier) Send(ctx context.Context, channel config.NotificationConfig, payload TaskEvent) error {
	switch strings.ToLower(strings.TrimSpace(channel.Type)) {
	case "", "onemsg_robot":
		return n.sendOneMsg(ctx, channel, payload)
	default:
		return &SendError{
			Message:   fmt.Sprintf("unsupported notification type %s", channel.Type),
			Retryable: false,
		}
	}
}

func (n *Notifier) Notify(ctx context.Context, siteName string, payload TaskEvent) error {
	channels := n.MatchingChannels(siteName, payload.Event)
	if len(channels) == 0 {
		return nil
	}

	var errs []error
	for _, channel := range channels {
		if err := n.Send(ctx, channel, payload); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", channel.Name, err))
		}
	}
	return errorsJoin(errs)
}

func (n *Notifier) sendOneMsg(ctx context.Context, channel config.NotificationConfig, payload TaskEvent) error {
	reqURL, robotKey, err := resolveOneMsgTarget(channel)
	if err != nil {
		return &SendError{
			Message:   err.Error(),
			Retryable: false,
		}
	}

	msg := formatOneMsgMessage(channel.TitlePrefix, payload)
	query := reqURL.Query()
	query.Set("robotKey", robotKey)
	query.Set("msg", msg)
	reqURL.RawQuery = query.Encode()

	timeout := channel.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return err
	}

	n.logger.Info("sending notification",
		"site_name", payload.SiteName,
		"event", string(payload.Event),
		"channel_name", channel.Name,
		"channel_type", "onemsg_robot",
		"endpoint", reqURL.Scheme+"://"+reqURL.Host+reqURL.Path,
	)

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	bodyText := strings.TrimSpace(string(respBody))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retryable := resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests
		return &SendError{
			Message:   fmt.Sprintf("notification status %d body=%s", resp.StatusCode, bodyText),
			Retryable: retryable,
		}
	}

	var result oneMsgResponse
	if len(respBody) > 0 && json.Unmarshal(respBody, &result) == nil {
		if !result.Success {
			if result.Code == 10002 || strings.Contains(result.Msg, "相隔1分钟") || strings.Contains(result.Msg, "相隔 1 分钟") {
				return &SendError{
					Message:    fmt.Sprintf("notification limited by OneMsg: code=%d msg=%s", result.Code, result.Msg),
					Retryable:  true,
					RetryAfter: maxDuration(channel.MinInterval, time.Minute),
				}
			}
			return &SendError{
				Message:   fmt.Sprintf("notification rejected by OneMsg: code=%d msg=%s", result.Code, result.Msg),
				Retryable: false,
			}
		}
	}

	n.logger.Info("notification sent successfully",
		"site_name", payload.SiteName,
		"event", string(payload.Event),
		"channel_name", channel.Name,
		"status_code", resp.StatusCode,
	)
	return nil
}

func RetryDecision(err error, retryInterval time.Duration) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}

	var sendErr *SendError
	if errors.As(err, &sendErr) {
		if !sendErr.Retryable {
			return 0, false
		}
		if sendErr.RetryAfter > 0 {
			return sendErr.RetryAfter, true
		}
	}

	if retryInterval <= 0 {
		retryInterval = 30 * time.Second
	}
	return retryInterval, true
}

func supportsEvent(events []string, event Event) bool {
	if len(events) == 0 {
		switch event {
		case EventQueued, EventDone, EventFailed, EventCallbackFailed:
			return true
		default:
			return false
		}
	}

	want := canonicalEventName(event)
	for _, candidate := range events {
		if canonicalEventName(Event(strings.TrimSpace(candidate))) == want {
			return true
		}
	}
	return false
}

func canonicalEventName(event Event) string {
	switch strings.ToLower(strings.TrimSpace(string(event))) {
	case string(EventCallbackPending):
		return string(EventCallbackFailed)
	default:
		return strings.ToLower(strings.TrimSpace(string(event)))
	}
}

func formatOneMsgMessage(titlePrefix string, payload TaskEvent) string {
	prefix := normalizeTitle(titlePrefix)
	if prefix == "" {
		prefix = "Harbor Relay"
	}

	title := map[Event]string{
		EventQueued:         "镜像同步已入队",
		EventPulling:        "镜像同步拉取中",
		EventPushing:        "镜像同步推送中",
		EventDone:           "镜像同步已完成",
		EventFailed:         "镜像同步失败",
		EventCallbackFailed: "镜像同步已完成，回调失败",
	}[payload.Event]
	if title == "" {
		title = "镜像同步状态更新"
	}

	sourceDisplay := pickDisplayRef(payload.SourceRefs, payload.SourcePullRef, payload.Repository, payload.Digest)
	targetDisplay := pickDisplayRef(payload.TargetRefDescriptors, firstNonEmpty(payload.TargetRefs...), payload.TargetRepository, payload.Digest)

	lines := []string{
		fmt.Sprintf("[%s] %s", prefix, title),
	}
	if payload.SiteName != "" {
		lines = append(lines, "站点: "+normalizeValue(payload.SiteName))
	}
	if payload.Channel != "" {
		lines = append(lines, "频道: "+normalizeValue(payload.Channel))
	}
	if sourceDisplay != "" {
		lines = append(lines, "源镜像: "+normalizeValue(sourceDisplay))
	}
	if targetDisplay != "" {
		lines = append(lines, "目标镜像: "+normalizeValue(targetDisplay))
	}
	if payload.Digest != "" {
		lines = append(lines, "摘要: "+normalizeValue(payload.Digest))
	}
	if len(payload.Tags) > 0 {
		lines = append(lines, "标签: "+normalizeValue(strings.Join(payload.Tags, ",")))
	}

	message := payload.Message
	if payload.Event == EventCallbackFailed && payload.CallbackMessage != "" {
		message = payload.CallbackMessage
	}
	if message != "" {
		lines = append(lines, "说明: "+normalizeValue(message))
	}
	lines = append(lines, "任务: "+normalizeValue(payload.TaskID))
	return strings.Join(lines, "\n")
}

func resolveOneMsgTarget(channel config.NotificationConfig) (*url.URL, string, error) {
	endpoint := strings.TrimSpace(channel.Endpoint)
	if endpoint == "" {
		endpoint = defaultOneMsgEndpoint
	}

	reqURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, "", fmt.Errorf("invalid endpoint: %w", err)
	}

	robotKey := strings.TrimSpace(channel.RobotKey)
	if robotKey == "" {
		robotKey = strings.TrimSpace(reqURL.Query().Get("robotKey"))
	}
	if robotKey == "" {
		return nil, "", fmt.Errorf("robot_key is required for onemsg_robot")
	}
	return reqURL, robotKey, nil
}

func pickDisplayRef(descriptors []string, direct, repository, digest string) string {
	switch {
	case len(descriptors) > 0 && strings.TrimSpace(descriptors[0]) != "":
		return descriptors[0]
	case strings.TrimSpace(direct) != "":
		return direct
	case strings.TrimSpace(repository) != "" && strings.TrimSpace(digest) != "":
		return repository + "@" + digest
	default:
		return repository
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func normalizeTitle(value string) string {
	replacer := strings.NewReplacer("\r", " ", "\n", " ", "\t", " ", "**", "", "##", "")
	return strings.Join(strings.Fields(replacer.Replace(strings.TrimSpace(value))), " ")
}

func normalizeValue(value string) string {
	replacer := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ", "**", "", "##", "")
	return strings.Join(strings.Fields(replacer.Replace(strings.TrimSpace(value))), " ")
}

func errorsJoin(errs []error) error {
	var parts []string
	for _, err := range errs {
		if err != nil {
			parts = append(parts, err.Error())
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return errors.New(strings.Join(parts, "; "))
}

func notificationLookupKey(siteName, channelName string) string {
	return strings.TrimSpace(siteName) + "::" + strings.TrimSpace(channelName)
}

func maxDuration(left, right time.Duration) time.Duration {
	if left > right {
		return left
	}
	return right
}
