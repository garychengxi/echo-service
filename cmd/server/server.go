package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"code.cestc.cn/ai-socket/logger-go"
	server "code.cestc.cn/ai-socket/server-go"
	"code.cestc.cn/ai-socket/server-go/version"
	"echo-service/internal/app/control"
	"echo-service/internal/bootstrap"
	"echo-service/internal/client"
	"echo-service/internal/config"
	"github.com/spf13/viper"
)

func main() {
	logger.SetDefaultConfig(&logger.Config{Outputs: []logger.Output{{Type: "Console", Level: "Info"}}})
	err := run()
	_ = logger.Sync()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "TOML config file (otherwise SERVER_CONFIG_PATH or conf/server/config.toml)")
	flag.Parse()
	if *configPath != "" {
		if err := os.Setenv("SERVER_CONFIG_PATH", *configPath); err != nil {
			return err
		}
	}
	// Preserve the reference bootstrap, config format and framework versions.
	boot := server.NewBootstrap()
	if err := boot.Initialize(); err != nil {
		return fmt.Errorf("bootstrap failed: check config path and TOML syntax")
	}
	defer boot.Close()
	cfg, err := config.Load(viper.GetViper())
	if err != nil {
		return err
	}
	clients, err := client.Open(cfg.Database)
	if err != nil {
		return err
	}
	defer func() {
		if err := clients.Close(); err != nil {
			logger.Error(context.Background(), "Infrastructure shutdown failed")
		}
	}()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	api, err := control.New(clients.Database, cfg)
	if err != nil {
		return fmt.Errorf("initialize service: %w", err)
	}
	defer api.Close()
	httpServer := bootstrap.NewServer(cfg, clients, version.AppVersion, api)
	logger.Infof(ctx, "echo-service listening on %s", httpServer.Addr)
	return bootstrap.Serve(ctx, httpServer, cfg.ShutdownTimeout)
}
