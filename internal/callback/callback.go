package callback

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Event describes one externally visible step in the sync lifecycle.
type Event string

const (
	EventQueued          Event = "queued"
	EventPulling         Event = "pulling"
	EventPushing         Event = "pushing"
	EventDone            Event = "done"
	EventFailed          Event = "failed"
	EventCallbackFailed  Event = "callback_failed"
	EventCallbackPending Event = "callback_pending" // legacy alias kept for config compatibility
)

// TaskEvent is the outbound payload shared by notifications and callbacks.
type TaskEvent struct {
	Event                Event             `json:"event"`
	TaskID               string            `json:"task_id"`
	EventID              string            `json:"event_id"`
	SiteName             string            `json:"site_name"`
	Channel              string            `json:"channel"`
	Status               string            `json:"status"`
	SourceRegistry       string            `json:"source_registry"`
	Repository           string            `json:"repository"`
	Digest               string            `json:"digest"`
	Tags                 []string          `json:"tags,omitempty"`
	SourcePullRef        string            `json:"source_pull_ref,omitempty"`
	SourceRefs           []string          `json:"source_refs,omitempty"`
	TargetRegistry       string            `json:"target_registry"`
	TargetRepository     string            `json:"target_repository"`
	TargetRefs           []string          `json:"target_refs,omitempty"`
	TargetRefDescriptors []string          `json:"target_ref_descriptors,omitempty"`
	Message              string            `json:"message,omitempty"`
	CallbackStatus       string            `json:"callback_status,omitempty"`
	CallbackMessage      string            `json:"callback_message,omitempty"`
	Metadata             map[string]string `json:"metadata,omitempty"`
	UpdatedAt            time.Time         `json:"updated_at"`
}

// Client sends JSON callbacks to external systems.
type Client struct {
	httpClient *http.Client
	logger     *slog.Logger
}

func NewClient(logger *slog.Logger) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		logger:     logger,
	}
}

func (c *Client) PostJSON(ctx context.Context, callbackURL, callbackToken string, payload TaskEvent) error {
	if strings.TrimSpace(callbackURL) == "" {
		return nil
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(callbackToken) != "" {
		req.Header.Set("Authorization", "Bearer "+callbackToken)
	}

	c.logger.Info("triggering callback",
		"task_id", payload.TaskID,
		"site_name", payload.SiteName,
		"event", string(payload.Event),
		"callback_url", callbackURL,
	)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("callback status %d body=%s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	c.logger.Info("callback finished successfully",
		"task_id", payload.TaskID,
		"site_name", payload.SiteName,
		"event", string(payload.Event),
		"status_code", resp.StatusCode,
	)
	return nil
}
