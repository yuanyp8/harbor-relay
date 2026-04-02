package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
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
	busy   atomic.Bool
}

var errSessionRefresh = errors.New("agent session refresh requested")

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
		if errors.Is(err, errSessionRefresh) {
			a.logger.Info("relay session rotated", "max_session_age", a.cfg.MaxSessionAge)
		} else {
			a.logger.Error("relay connection ended", "err", err)
		}
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
	sessionStartedAt := time.Now()

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
	a.logger.Info("relay stream connected",
		"relay_address", a.cfg.RelayAddress,
		"site_name", a.cfg.SiteName,
		"channels", a.cfg.Channels,
		"max_session_age", a.cfg.MaxSessionAge,
	)

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
			a.logger.Debug("sending agent heartbeat",
				"agent_id", a.cfg.AgentID,
				"site_name", a.cfg.SiteName,
			)
			if err := stream.Send(&relayv1.AgentMessage{
				Payload: &relayv1.AgentMessage_Heartbeat{
					Heartbeat: &relayv1.AgentHeartbeat{
						UnixTime: time.Now().Unix(),
					},
				},
			}); err != nil {
				return err
			}
			if a.cfg.MaxSessionAge > 0 && time.Since(sessionStartedAt) >= a.cfg.MaxSessionAge {
				if a.busy.Load() {
					a.logger.Debug("max session age reached but agent is busy; postpone reconnect",
						"agent_id", a.cfg.AgentID,
						"max_session_age", a.cfg.MaxSessionAge,
					)
					continue
				}
				a.logger.Info("max session age reached; reconnecting idle agent stream",
					"agent_id", a.cfg.AgentID,
					"max_session_age", a.cfg.MaxSessionAge,
				)
				return errSessionRefresh
			}
		}
	}
}

// handleTask 执行真正的镜像同步动作。
// relay 告诉 agent “同步什么”，agent 只负责按固定顺序执行：
// login -> pull by digest -> tag -> push -> report
func (a *Agent) handleTask(ctx context.Context, stream relayv1.RelayService_ConnectClient, task *relayv1.TaskAssignment) error {
	a.busy.Store(true)
	defer a.busy.Store(false)
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

	delayTargetLogin := shouldDelayTargetLogin(task, a.cfg)
	if delayTargetLogin {
		a.logger.Info("source and target registry are identical; using sequential login flow",
			"registry", task.SourceRegistry,
			"source_username", a.cfg.SourceUsername,
			"target_username", a.cfg.TargetUsername,
		)
	}

	if err := a.login(ctx, task.SourceRegistry, a.cfg.SourceUsername, a.cfg.SourcePassword); err != nil {
		return a.sendProgress(stream, task.TaskId, relayv1.TaskStatus_TASK_STATUS_FAILED, "source login failed: "+err.Error(), nil, nil)
	}
	if !delayTargetLogin {
		if err := a.login(ctx, task.TargetRegistry, a.cfg.TargetUsername, a.cfg.TargetPassword); err != nil {
			return a.sendProgress(stream, task.TaskId, relayv1.TaskStatus_TASK_STATUS_FAILED, "target login failed: "+err.Error(), nil, nil)
		}
	}

	sourceRef := task.SourcePullRef
	if sourceRef == "" {
		sourceRef = fmt.Sprintf("%s/%s@%s", task.SourceRegistry, task.Repository, task.Digest)
	}
	if err := a.runDocker(ctx, "pull", sourceRef); err != nil {
		return a.sendProgress(stream, task.TaskId, relayv1.TaskStatus_TASK_STATUS_FAILED, "docker pull failed: "+err.Error(), nil, nil)
	}

	if delayTargetLogin {
		if err := a.login(ctx, task.TargetRegistry, a.cfg.TargetUsername, a.cfg.TargetPassword); err != nil {
			return a.sendProgress(stream, task.TaskId, relayv1.TaskStatus_TASK_STATUS_FAILED, "target login failed: "+err.Error(), nil, nil)
		}
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
	a.logger.Debug("sending task progress",
		"task_id", taskID,
		"status", status.String(),
		"message", message,
		"target_refs", refs,
		"target_ref_descriptors", descriptors,
	)
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
	if err := a.ensureDockerConfigDir(); err != nil {
		return err
	}
	a.logger.Debug("docker login started", "registry", registry, "username", username)
	cmd := exec.CommandContext(ctx, a.cfg.DockerBinary, a.loginArgs(registry, username)...)
	cmd.Env = a.commandEnv()
	cmd.Stdin = strings.NewReader(password)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	a.logger.Debug("docker login completed", "registry", registry, "username", username)
	return nil
}

// runDocker 是对 docker CLI 的最薄封装，方便后面补测试桩或替换实现。
func (a *Agent) runDocker(ctx context.Context, args ...string) error {
	if err := a.ensureDockerConfigDir(); err != nil {
		return err
	}
	a.logger.Debug("docker command started", "args", args)
	cmd := exec.CommandContext(ctx, a.cfg.DockerBinary, a.runtimeArgs(args...)...)
	cmd.Env = a.commandEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	a.logger.Debug("docker command completed", "args", args)
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

func shouldDelayTargetLogin(task *relayv1.TaskAssignment, cfg config.AgentConfig) bool {
	if !sameRegistryHost(task.SourceRegistry, task.TargetRegistry) {
		return false
	}
	return cfg.SourceUsername != cfg.TargetUsername || cfg.SourcePassword != cfg.TargetPassword
}

func sameRegistryHost(left, right string) bool {
	return normalizeRegistryHost(left) == normalizeRegistryHost(right)
}

func normalizeRegistryHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimSuffix(host, "/")
	return host
}

func (a *Agent) loginArgs(registry, username string) []string {
	switch a.runtimeKind() {
	case "sealos":
		return []string{"login", "-u", username, "--password-stdin", registry}
	default:
		return a.runtimeArgs("login", registry, "-u", username, "--password-stdin")
	}
}

func (a *Agent) runtimeArgs(args ...string) []string {
	if a.cfg.DockerConfigDir == "" {
		return args
	}
	switch a.runtimeKind() {
	case "sealos":
		return args
	default:
		result := []string{"--config", a.cfg.DockerConfigDir}
		result = append(result, args...)
		return result
	}
}

func (a *Agent) commandEnv() []string {
	env := os.Environ()
	if a.cfg.DockerConfigDir == "" {
		return env
	}
	switch a.runtimeKind() {
	case "sealos":
		return append(env, "REGISTRY_AUTH_FILE="+a.sealosAuthFile())
	default:
		return env
	}
}

func (a *Agent) runtimeKind() string {
	name := strings.ToLower(filepath.Base(strings.TrimSpace(a.cfg.DockerBinary)))
	switch {
	case strings.Contains(name, "sealos"):
		return "sealos"
	default:
		return "docker"
	}
}

func (a *Agent) sealosAuthFile() string {
	if a.cfg.DockerConfigDir == "" {
		return ""
	}
	return filepath.Join(a.cfg.DockerConfigDir, "auth.json")
}

func (a *Agent) ensureDockerConfigDir() error {
	if a.cfg.DockerConfigDir == "" {
		return nil
	}
	return os.MkdirAll(a.cfg.DockerConfigDir, 0o700)
}
