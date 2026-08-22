// Command nzbget-bot runs the Telegram bot that drives an NZBGet instance.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/avikekkk/nzbget-usenet-bot-go/internal/bot"
	"github.com/avikekkk/nzbget-usenet-bot-go/internal/config"
	"github.com/avikekkk/nzbget-usenet-bot-go/internal/nzbget"
	"github.com/avikekkk/nzbget-usenet-bot-go/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	// Loaded before logging is configured so DATA_DIR can place the log file.
	if err := config.LoadEnvFile("config.env"); err != nil {
		return fmt.Errorf("read config.env: %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logFile, err := openLog(cfg.LogFile)
	if err != nil {
		return err
	}
	defer logFile.Close()

	logger := slog.New(slog.NewTextHandler(io.MultiWriter(os.Stdout, logFile), &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	nzb := nzbget.New(cfg.NZBGetBase)
	version, err := nzb.Version(ctx)
	if err != nil {
		return fmt.Errorf("failed to load NZBGet: %w", err)
	}
	logger.Info("Loaded NZBGet", "version", version)

	db, err := store.Open(cfg.DBFile)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	if err := bot.New(cfg, logger, db, nzb).Run(ctx); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// openLog starts a fresh log each run, matching the previous behaviour of
// removing log.txt at startup so /logs returns only the current session.
func openLog(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	return file, nil
}
