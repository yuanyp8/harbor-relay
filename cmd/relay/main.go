package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

	relayv1 "github.com/yuanyp8/harbor-relay/gen/proto/relay/v1"
	"github.com/yuanyp8/harbor-relay/internal/config"
	"github.com/yuanyp8/harbor-relay/internal/logutil"
	"github.com/yuanyp8/harbor-relay/internal/relay"
)

const grpcShutdownTimeout = 8 * time.Second

type grpcStopper interface {
	GracefulStop()
	Stop()
}

func main() {
	configPath := flag.String("config", "/etc/harbor-relay/relay.yaml", "path to the relay config file")
	flag.Parse()

	bootstrapLogger := logutil.New("relay", "info", "text")

	cfg, err := config.LoadRelayConfig(*configPath)
	if err != nil {
		bootstrapLogger.Error("load config failed", "err", err)
		os.Exit(1)
	}
	logger := logutil.New("relay", cfg.LogLevel, cfg.LogFormat)

	store, err := relay.NewStore(cfg.DataFile)
	if err != nil {
		logger.Error("init store failed", "err", err)
		os.Exit(1)
	}

	service := relay.NewService(cfg, store, logger)
	runtimeCtx, runtimeCancel := context.WithCancel(context.Background())
	defer runtimeCancel()
	service.StartBackground(runtimeCtx)

	grpcServer := grpc.NewServer()
	relayv1.RegisterRelayServiceServer(grpcServer, relay.NewGRPCServer(service, logger))

	grpcListener, err := net.Listen("tcp", cfg.GRPCListen)
	if err != nil {
		logger.Error("grpc listen failed", "err", err, "addr", cfg.GRPCListen)
		os.Exit(1)
	}

	httpServer := &http.Server{
		Addr:              cfg.HTTPListen,
		Handler:           service.HTTPHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	httpErrCh := make(chan error, 1)
	grpcErrCh := make(chan error, 1)

	go func() {
		logger.Info("http server started", "addr", cfg.HTTPListen)
		httpErrCh <- httpServer.ListenAndServe()
	}()

	go func() {
		logger.Info("grpc server started", "addr", cfg.GRPCListen)
		grpcErrCh <- grpcServer.Serve(grpcListener)
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-stop:
		logger.Info("shutdown requested", "signal", sig.String())
	case err := <-httpErrCh:
		if err != nil && err != http.ErrServerClosed {
			logger.Error("http server failed", "err", err)
		}
	case err := <-grpcErrCh:
		if err != nil {
			logger.Error("grpc server failed", "err", err)
		}
	}

	runtimeCancel()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Warn("http server shutdown returned error", "err", err)
	}
	stopGRPCServer(logger, grpcServer, grpcShutdownTimeout)
}

// stopGRPCServer 先尝试优雅关闭 gRPC 服务，给 agent 长连接一个尽量平滑的退出窗口。
// 如果超时仍有双向流未结束，则主动调用 Stop 断开连接，避免 systemd restart 卡在 stop-sigterm。
func stopGRPCServer(logger *slog.Logger, server grpcStopper, timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("grpc server stopped gracefully", "timeout", timeout)
	case <-time.After(timeout):
		logger.Warn("grpc graceful stop timed out; forcing stop", "timeout", timeout)
		server.Stop()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			logger.Warn("grpc server stop did not finish promptly after force stop")
		}
	}
}
