package relay

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	relayv1 "github.com/yuanyp8/harbor-relay/gen/proto/relay/v1"
)

// GRPCServer 对外暴露 Agent <-> Relay 的双向流接口。
type GRPCServer struct {
	relayv1.UnimplementedRelayServiceServer
	service *Service
	logger  *slog.Logger
}

// Connect 是 relay 和远端 agent 的长连接主循环。
// agent 先发送 hello 注册，再持续发送 heartbeat/progress；
// relay 只要发现 site 和 channel 匹配的待处理任务，就会主动派发。
func NewGRPCServer(service *Service, logger *slog.Logger) *GRPCServer {
	return &GRPCServer{
		service: service,
		logger:  logger,
	}
}

func (s *GRPCServer) Connect(stream relayv1.RelayService_ConnectServer) error {
	ctx := stream.Context()

	firstMsg, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "failed to receive hello: %v", err)
	}
	hello := firstMsg.GetHello()
	if hello == nil {
		return status.Error(codes.InvalidArgument, "first message must be hello")
	}

	agent := &Agent{
		AgentID:      hello.AgentId,
		SiteName:     hello.SiteName,
		Channels:     hello.Channels,
		Version:      hello.Version,
		Capabilities: hello.Capabilities,
		Online:       true,
		LastSeenAt:   time.Now(),
		ConnectedAt:  time.Now(),
	}
	s.logger.Info("agent hello received",
		"agent_id", agent.AgentID,
		"site_name", agent.SiteName,
		"channels", agent.Channels,
		"version", agent.Version,
		"capabilities", agent.Capabilities,
	)
	if err := s.service.store.UpsertAgent(agent); err != nil {
		return status.Errorf(codes.Internal, "failed to upsert agent: %v", err)
	}

	if err := stream.Send(&relayv1.RelayMessage{
		Payload: &relayv1.RelayMessage_Ack{
			Ack: &relayv1.RelayAck{
				Message: "registered",
			},
		},
	}); err != nil {
		return err
	}

	if err := s.maybeSendTask(stream, hello.SiteName, hello.Channels, hello.AgentId); err != nil {
		return err
	}

	defer func() {
		s.logger.Info("agent disconnected", "agent_id", hello.AgentId, "site_name", hello.SiteName)
		if err := s.service.store.MarkAgentOffline(hello.AgentId); err != nil {
			s.logger.Error("mark agent offline failed", "agent_id", hello.AgentId, "err", err)
		}
	}()

	for {
		msg, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}

		switch payload := msg.Payload.(type) {
		case *relayv1.AgentMessage_Heartbeat:
			s.logger.Debug("agent heartbeat received",
				"agent_id", hello.AgentId,
				"site_name", hello.SiteName,
			)
			if err := s.service.store.MarkHeartbeat(hello.AgentId); err != nil {
				return status.Errorf(codes.Internal, "heartbeat failed: %v", err)
			}
			if err := s.maybeSendTask(stream, hello.SiteName, hello.Channels, hello.AgentId); err != nil {
				return err
			}
		case *relayv1.AgentMessage_Progress:
			s.logger.Info("agent progress received",
				"agent_id", hello.AgentId,
				"task_id", payload.Progress.TaskId,
				"status", payload.Progress.Status.String(),
				"message", payload.Progress.Message,
				"target_refs", payload.Progress.TargetRefs,
				"target_ref_descriptors", payload.Progress.TargetRefDescriptors,
			)
			task, err := s.service.store.UpdateTaskProgress(
				hello.AgentId,
				payload.Progress.TaskId,
				payload.Progress.Status,
				payload.Progress.Message,
				payload.Progress.TargetRefs,
				payload.Progress.TargetRefDescriptors,
			)
			if err != nil {
				return status.Errorf(codes.Internal, "update progress failed: %v", err)
			}

			if payload.Progress.Status == relayv1.TaskStatus_TASK_STATUS_DONE && task.CallbackURL != "" {
				if cbErr := s.service.TriggerCallback(ctx, task); cbErr != nil {
					_, _ = s.service.store.UpdateTaskProgress(
						hello.AgentId,
						payload.Progress.TaskId,
						relayv1.TaskStatus_TASK_STATUS_CALLBACK_PENDING,
						"callback failed: "+cbErr.Error(),
						payload.Progress.TargetRefs,
						payload.Progress.TargetRefDescriptors,
					)
					s.logger.Error("callback failed", "task_id", task.ID, "err", cbErr)
				}
			}

			if err := s.maybeSendTask(stream, hello.SiteName, hello.Channels, hello.AgentId); err != nil {
				return err
			}
		default:
			s.logger.Warn("unsupported agent payload", "agent_id", hello.AgentId)
		}
	}
}

// maybeSendTask 在 agent 连上或发心跳后尝试分配一个新任务。
// 如果当前没有可分配任务，就保持静默，让连接继续挂着。
func (s *GRPCServer) maybeSendTask(stream relayv1.RelayService_ConnectServer, siteName string, channels []string, agentID string) error {
	task, err := s.service.store.AssignNextTask(siteName, channels, agentID)
	if err != nil {
		return status.Errorf(codes.Internal, "assign task failed: %v", err)
	}
	if task == nil {
		totalPending, sameSitePending, assignablePending := s.service.store.PendingTaskStats(siteName, channels)
		s.logger.Debug("no task available for agent",
			"agent_id", agentID,
			"site_name", siteName,
			"channels", channels,
			"total_pending", totalPending,
			"same_site_pending", sameSitePending,
			"assignable_pending", assignablePending,
		)
		return nil
	}
	s.logger.Info("task assigned to agent",
		"agent_id", agentID,
		"site_name", siteName,
		"task_id", task.ID,
		"channel", task.Channel,
		"repository", task.Repository,
		"digest", task.Digest,
		"tags", task.Tags,
		"source_pull_ref", task.SourcePullRef,
		"source_refs", task.SourceRefs,
		"target_repository", task.TargetRepository,
	)

	return stream.Send(&relayv1.RelayMessage{
		Payload: &relayv1.RelayMessage_Task{
			Task: &relayv1.TaskAssignment{
				TaskId:           task.ID,
				EventId:          task.EventID,
				SiteName:         task.SiteName,
				SourceRegistry:   task.SourceRegistry,
				Repository:       task.Repository,
				Digest:           task.Digest,
				Tags:             task.Tags,
				TargetRegistry:   task.TargetRegistry,
				TargetRepository: task.TargetRepository,
				CallbackUrl:      task.CallbackURL,
				Metadata:         task.Metadata,
				Channel:          task.Channel,
				SourcePullRef:    task.SourcePullRef,
				SourceRefs:       task.SourceRefs,
			},
		},
	})
}
