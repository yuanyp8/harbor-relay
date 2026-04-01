package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/yuanyp8/harbor-relay/internal/agent"
	"github.com/yuanyp8/harbor-relay/internal/config"
	"github.com/yuanyp8/harbor-relay/internal/logutil"
)

func main() {
	configPath := flag.String("config", "./configs/agent.yaml", "agent 配置文件路径")
	flag.Parse()

	bootstrapLogger := logutil.New("agent", "info", "text")

	cfg, err := config.LoadAgentConfig(*configPath)
	if err != nil {
		bootstrapLogger.Error("load config failed", "err", err)
		os.Exit(1)
	}
	logger := logutil.New("agent", cfg.LogLevel, cfg.LogFormat)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app := agent.New(cfg, logger)
	if err := app.Run(ctx); err != nil && ctx.Err() == nil {
		logger.Error("agent exited", "err", err)
		os.Exit(1)
	}
}
