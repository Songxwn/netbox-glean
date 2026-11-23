// Command netbox-glean collects hardware inventory over SNMP, IPMI and
// Redfish and writes it into NetBox.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Songxwn/netbox-glean/internal/config"
	"github.com/Songxwn/netbox-glean/internal/jobs"
	"github.com/Songxwn/netbox-glean/internal/server"
	"github.com/Songxwn/netbox-glean/internal/store"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "netbox-glean:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.ShowVersion {
		fmt.Printf("netbox-glean %s (commit %s, built %s)\n", version, commit, date)
		return nil
	}

	level := slog.LevelInfo
	if cfg.Debug {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	if err := st.Bootstrap(cfg.NetboxURL, cfg.NetboxToken, cfg.Site, cfg.DeviceRole); err != nil {
		return fmt.Errorf("初始化设置失败: %w", err)
	}

	manager := jobs.New(jobs.Options{
		MaxConcurrent: cfg.Concurrency,
		Timeout:       cfg.Timeout,
		Retain:        200,
	})

	handler := server.New(server.Options{
		Store:       st,
		Jobs:        manager,
		Version:     version,
		Commit:      commit,
		Date:        date,
		WebUsername: cfg.WebUsername,
		WebPassword: cfg.WebPassword,
		Logger:      logger,
	})

	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("netbox-glean started", "version", version, "config", cfg.Describe())
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errCh:
		return fmt.Errorf("HTTP 服务失败: %w", err)
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("关闭 HTTP 服务失败: %w", err)
	}
	return nil
}