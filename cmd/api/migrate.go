package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"houseflowApi/internal/config"
	"houseflowApi/internal/data/database"
	"houseflowApi/internal/data/migrations"
)

const migrationTimeout = 4 * time.Minute

func runMigrations() error {
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ctx, cancel := context.WithTimeout(signalCtx, migrationTimeout)
	defer cancel()

	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := config.ValidateMongo(cfg.External.Mongo); err != nil {
		return fmt.Errorf("validate mongo config: %w", err)
	}

	client, db, err := database.NewDatabase(ctx, cfg.External.Mongo)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer disconnectDatabase(client)

	if err := migrations.RunAll(ctx, db, migrations.AllMigrations()); err != nil {
		return err
	}

	log.Println("migrations completed")
	return nil
}
