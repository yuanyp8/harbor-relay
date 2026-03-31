package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/yuanyp8/harbor-relay/internal/agent"
	"github.com/yuanyp8/harbor-relay/internal/config"
)

func main() {
	configPath := flag.String("config", "./configs/agent.yaml", "agent 配置文件路径")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.LoadAgentConfig(*configPath)
	if err != nil {
		logger.Error("load config failed", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app := agent.New(cfg, logger)
	if err := app.Run(ctx); err != nil && ctx.Err() == nil {
		logger.Error("agent exited", "err", err)
		os.Exit(1)
	}
}
