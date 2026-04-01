package main

import (
	"context"
	"flag"
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

func main() {
	configPath := flag.String("config", "./configs/relay.yaml", "relay 配置文件路径")
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

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
	grpcServer.GracefulStop()
}
