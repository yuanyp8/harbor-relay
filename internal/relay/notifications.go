package relay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	callbackmod "github.com/yuanyp8/harbor-relay/internal/callback"
	"github.com/yuanyp8/harbor-relay/internal/config"
)

const notificationDispatcherInterval = time.Second

// StartBackground launches the internal notification dispatcher.
// Webhook and gRPC handlers only enqueue jobs; actual sending is rate-limited
// here so OneMsg restrictions do not block the main sync path.
func (s *Service) StartBackground(ctx context.Context) {
	if s.notifier == nil {
		return
	}

	go s.runNotificationDispatcher(ctx, notificationDispatcherInterval)
}

func (s *Service) runNotificationDispatcher(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.logger.Info("notification dispatcher started", "interval", interval.String())
	for {
		if err := s.processNotificationQueueOnce(ctx); err != nil {
			s.logger.Error("notification dispatcher loop failed", "err", err)
		}

		select {
		case <-ctx.Done():
			s.logger.Info("notification dispatcher stopped")
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) processNotificationQueueOnce(ctx context.Context) error {
	if s.notifier == nil {
		return nil
	}

	jobs := s.store.ListDueNotificationJobs(time.Now(), 100)
	for _, job := range jobs {
		if err := s.dispatchNotificationJob(ctx, job); err != nil {
			s.logger.Error("notification job processing failed",
				"job_id", job.ID,
				"task_id", job.TaskID,
				"channel_name", job.ChannelName,
				"err", err,
			)
		}
	}
	return nil
}

func (s *Service) dispatchNotificationJob(ctx context.Context, job *NotificationJob) error {
	channel, ok := s.notifier.GetChannel(job.SiteName, job.ChannelName)
	if !ok {
		err := fmt.Errorf("notification channel not found: site=%s channel=%s", job.SiteName, job.ChannelName)
		if markErr := s.store.MarkNotificationJobFailed(job.ID, err.Error()); markErr != nil {
			return fmt.Errorf("%w; mark failed: %v", err, markErr)
		}
		return err
	}
	if !channel.IsEnabled() {
		err := fmt.Errorf("notification channel is disabled: site=%s channel=%s", job.SiteName, job.ChannelName)
		if markErr := s.store.MarkNotificationJobFailed(job.ID, err.Error()); markErr != nil {
			return fmt.Errorf("%w; mark failed: %v", err, markErr)
		}
		return err
	}

	now := time.Now()
	if state, ok := s.store.GetNotificationChannelState(job.ChannelKey); ok && state.NextAllowedAt.After(now) {
		s.logger.Debug("notification job skipped because channel is cooling down",
			"job_id", job.ID,
			"task_id", job.TaskID,
			"channel_name", job.ChannelName,
			"next_allowed_at", state.NextAllowedAt,
		)
		return nil
	}

	s.logger.Info("notification job sending",
		"job_id", job.ID,
		"task_id", job.TaskID,
		"site_name", job.SiteName,
		"channel_name", job.ChannelName,
		"event", job.Event,
		"attempts", job.Attempts,
	)

	if err := s.notifier.Send(ctx, channel, job.Payload); err != nil {
		return s.handleNotificationFailure(job, channel, err)
	}

	nextAllowedAt := now.Add(channel.MinInterval)
	if err := s.store.MarkNotificationJobDelivered(job.ID, job.TaskID, job.ReceiptKey, now, nextAllowedAt); err != nil {
		return err
	}

	s.logger.Info("notification job delivered",
		"job_id", job.ID,
		"task_id", job.TaskID,
		"site_name", job.SiteName,
		"channel_name", job.ChannelName,
		"event", job.Event,
		"next_allowed_at", nextAllowedAt,
	)
	return nil
}

func (s *Service) handleNotificationFailure(job *NotificationJob, channel config.NotificationConfig, err error) error {
	retryAfter, retryable := callbackmod.RetryDecision(err, channel.RetryInterval)
	attempt := job.Attempts + 1

	if retryable && !exceededMaxAttempts(channel.MaxAttempts, attempt) {
		nextAttemptAt := time.Now().Add(retryAfter)
		if storeErr := s.store.RescheduleNotificationJob(job.ID, nextAttemptAt, err.Error(), nextAttemptAt); storeErr != nil {
			return fmt.Errorf("%w; reschedule failed: %v", err, storeErr)
		}
		s.logger.Warn("notification job rescheduled",
			"job_id", job.ID,
			"task_id", job.TaskID,
			"site_name", job.SiteName,
			"channel_name", job.ChannelName,
			"event", job.Event,
			"attempt", attempt,
			"next_attempt_at", nextAttemptAt,
			"reason", err.Error(),
		)
		return nil
	}

	if storeErr := s.store.MarkNotificationJobFailed(job.ID, err.Error()); storeErr != nil {
		return fmt.Errorf("%w; mark failed: %v", err, storeErr)
	}
	s.logger.Error("notification job failed permanently",
		"job_id", job.ID,
		"task_id", job.TaskID,
		"site_name", job.SiteName,
		"channel_name", job.ChannelName,
		"event", job.Event,
		"attempt", attempt,
		"reason", err.Error(),
	)
	return nil
}

func buildNotificationReceiptKey(event callbackmod.Event, channelName string) string {
	return strings.TrimSpace(string(event)) + "::" + strings.TrimSpace(channelName)
}

func buildNotificationChannelKey(siteName, channelName string) string {
	return strings.TrimSpace(siteName) + "::" + strings.TrimSpace(channelName)
}

func buildNotificationJobID(taskID, receiptKey string) string {
	sum := sha256.Sum256([]byte(taskID + "::" + receiptKey))
	return hex.EncodeToString(sum[:])[:24]
}

func exceededMaxAttempts(maxAttempts, attempt int) bool {
	if maxAttempts <= 0 {
		return false
	}
	return attempt >= maxAttempts
}
