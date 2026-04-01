package agent

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	relayv1 "github.com/yuanyp8/harbor-relay/gen/proto/relay/v1"
	"github.com/yuanyp8/harbor-relay/internal/config"
)

// Agent 是部署在远端 DC 的轻量消费端。
// 它不做调度决策，只做确定性的镜像搬运和状态回报。
type Agent struct {
	cfg    config.AgentConfig
	logger *slog.Logger
}

// Run 维持与 relay 的长连接。
// 远端节点只需要能主动连出；真正的调度决策都在 relay 侧完成。
func New(cfg config.AgentConfig, logger *slog.Logger) *Agent {
	return &Agent{cfg: cfg, logger: logger}
}

func (a *Agent) Run(ctx context.Context) error {
	for {
		err := a.runOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		a.logger.Error("relay connection ended", "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(a.cfg.ReconnectInterval):
		}
	}
}

func (a *Agent) runOnce(ctx context.Context) error {
	conn, err := a.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := relayv1.NewRelayServiceClient(conn)
	stream, err := client.Connect(ctx)
	if err != nil {
		return err
	}

	if err := stream.Send(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Hello{
			Hello: &relayv1.AgentHello{
				AgentId:      a.cfg.AgentID,
				SiteName:     a.cfg.SiteName,
				Channels:     a.cfg.Channels,
				Version:      a.cfg.Version,
				Capabilities: []string{"docker-sync"},
			},
		},
	}); err != nil {
		return err
	}

	heartbeatTicker := time.NewTicker(a.cfg.HeartbeatInterval)
	defer heartbeatTicker.Stop()

	errCh := make(chan error, 1)
	go func() {
		for {
			msg, recvErr := stream.Recv()
			if recvErr != nil {
				errCh <- recvErr
				return
			}
			if task := msg.GetTask(); task != nil {
				if handleErr := a.handleTask(ctx, stream, task); handleErr != nil {
					a.logger.Error("handle task failed", "task_id", task.TaskId, "err", handleErr)
				}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			return err
		case <-heartbeatTicker.C:
			if err := stream.Send(&relayv1.AgentMessage{
				Payload: &relayv1.AgentMessage_Heartbeat{
					Heartbeat: &relayv1.AgentHeartbeat{
						UnixTime: time.Now().Unix(),
					},
				},
			}); err != nil {
				return err
			}
		}
	}
}

// handleTask 执行真正的镜像同步动作。
// relay 告诉 agent “同步什么”，agent 只负责按固定顺序执行：
// login -> pull by digest -> tag -> push -> report
func (a *Agent) handleTask(ctx context.Context, stream relayv1.RelayService_ConnectClient, task *relayv1.TaskAssignment) error {
	a.logger.Info("received task",
		"task_id", task.TaskId,
		"channel", task.Channel,
		"repository", task.Repository,
		"digest", task.Digest,
		"tags", task.Tags,
		"source_pull_ref", task.SourcePullRef,
		"source_refs", task.SourceRefs,
		"target_repository", task.TargetRepository,
	)

	if err := a.sendProgress(stream, task.TaskId, relayv1.TaskStatus_TASK_STATUS_PULLING, "pulling source image", nil, nil); err != nil {
		return err
	}

	if err := a.login(ctx, task.SourceRegistry, a.cfg.SourceUsername, a.cfg.SourcePassword); err != nil {
		return a.sendProgress(stream, task.TaskId, relayv1.TaskStatus_TASK_STATUS_FAILED, "source login failed: "+err.Error(), nil, nil)
	}
	if err := a.login(ctx, task.TargetRegistry, a.cfg.TargetUsername, a.cfg.TargetPassword); err != nil {
		return a.sendProgress(stream, task.TaskId, relayv1.TaskStatus_TASK_STATUS_FAILED, "target login failed: "+err.Error(), nil, nil)
	}

	sourceRef := task.SourcePullRef
	if sourceRef == "" {
		sourceRef = fmt.Sprintf("%s/%s@%s", task.SourceRegistry, task.Repository, task.Digest)
	}
	if err := a.runDocker(ctx, "pull", sourceRef); err != nil {
		return a.sendProgress(stream, task.TaskId, relayv1.TaskStatus_TASK_STATUS_FAILED, "docker pull failed: "+err.Error(), nil, nil)
	}

	if len(task.Tags) == 0 {
		return a.sendProgress(stream, task.TaskId, relayv1.TaskStatus_TASK_STATUS_FAILED, "no usable tags found in task", nil, nil)
	}

	if err := a.sendProgress(stream, task.TaskId, relayv1.TaskStatus_TASK_STATUS_PUSHING, "pushing target image", nil, nil); err != nil {
		return err
	}

	targetRefs := make([]string, 0, len(task.Tags))
	targetRefDescriptors := make([]string, 0, len(task.Tags))
	for _, tag := range task.Tags {
		targetRef := fmt.Sprintf("%s/%s:%s", task.TargetRegistry, task.TargetRepository, tag)
		if err := a.runDocker(ctx, "tag", sourceRef, targetRef); err != nil {
			return a.sendProgress(stream, task.TaskId, relayv1.TaskStatus_TASK_STATUS_FAILED, "docker tag failed: "+err.Error(), targetRefs, targetRefDescriptors)
		}
		if err := a.runDocker(ctx, "push", targetRef); err != nil {
			return a.sendProgress(stream, task.TaskId, relayv1.TaskStatus_TASK_STATUS_FAILED, "docker push failed: "+err.Error(), targetRefs, targetRefDescriptors)
		}
		targetRefs = append(targetRefs, targetRef)
		targetRefDescriptors = append(targetRefDescriptors, fmt.Sprintf("%s@%s", targetRef, task.Digest))
		a.logger.Info("target ref pushed",
			"task_id", task.TaskId,
			"target_ref", targetRef,
			"target_ref_descriptor", targetRefDescriptors[len(targetRefDescriptors)-1],
		)
	}

	if a.cfg.CleanupLocalImages {
		for _, targetRef := range targetRefs {
			_ = a.runDocker(ctx, "image", "rm", "-f", targetRef)
		}
		_ = a.runDocker(ctx, "image", "rm", "-f", sourceRef)
	}

	return a.sendProgress(stream, task.TaskId, relayv1.TaskStatus_TASK_STATUS_DONE, "image sync completed", targetRefs, targetRefDescriptors)
}

// sendProgress 把当前任务进度回报给 relay。
func (a *Agent) sendProgress(stream relayv1.RelayService_ConnectClient, taskID string, status relayv1.TaskStatus, message string, refs, descriptors []string) error {
	return stream.Send(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Progress{
			Progress: &relayv1.TaskProgress{
				TaskId:               taskID,
				Status:               status,
				Message:              message,
				TargetRefs:           refs,
				TargetRefDescriptors: descriptors,
			},
		},
	})
}

// login 支持源仓库和目标仓库分别登录。
func (a *Agent) login(ctx context.Context, registry, username, password string) error {
	if registry == "" || username == "" || password == "" {
		return nil
	}
	cmd := exec.CommandContext(ctx, a.cfg.DockerBinary, "login", registry, "-u", username, "--password-stdin")
	cmd.Stdin = strings.NewReader(password)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// runDocker 是对 docker CLI 的最薄封装，方便后面补测试桩或替换实现。
func (a *Agent) runDocker(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, a.cfg.DockerBinary, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// dial 根据配置决定 TLS / 跳过校验 / 本地明文三种连接方式。
func (a *Agent) dial(ctx context.Context) (*grpc.ClientConn, error) {
	if a.cfg.InsecureSkipVerify {
		return grpc.NewClient(a.cfg.RelayAddress,
			grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
				ServerName:         a.cfg.RelayServerName,
				InsecureSkipVerify: true,
			})),
		)
	}
	if a.cfg.RelayServerName != "" {
		return grpc.NewClient(a.cfg.RelayAddress,
			grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
				ServerName: a.cfg.RelayServerName,
			})),
		)
	}
	if strings.HasPrefix(a.cfg.RelayAddress, "127.0.0.1:") || strings.HasPrefix(a.cfg.RelayAddress, "localhost:") {
		return grpc.NewClient(a.cfg.RelayAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	return grpc.NewClient(a.cfg.RelayAddress, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})))
}
