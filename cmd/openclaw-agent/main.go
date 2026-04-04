package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	appconfig "github.com/iamlovingit/clawmanager-openclaw-image/internal/config"
	"github.com/iamlovingit/clawmanager-openclaw-image/internal/supervisor"
)

func main() {
	cfg, err := appconfig.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	s, err := supervisor.New(cfg)
	if err != nil {
		log.Fatalf("init supervisor: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := s.Run(ctx); err != nil {
		log.Fatalf("run supervisor: %v", err)
	}
}
