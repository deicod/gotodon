// Package cmd provides CLI commands for the gotodon server.
package cmd

import (
	"context"
	"database/sql"
	"log"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/spf13/cobra"

	"github.com/deicod/gotodon/db/migrations"
	"github.com/deicod/gotodon/internal/config"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Run database migrations",
	Long:  "Run database migrations using goose",
	Run:   runMigrate,
}

var migrateUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Migrate the DB to the most recent version available",
	Run:   runMigrateUp,
}

var migrateDownCmd = &cobra.Command{
	Use:   "down",
	Short: "Roll back the version by 1",
	Run:   runMigrateDown,
}

var migrateStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show migration status",
	Run:   runMigrateStatus,
}

var migrateResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Roll back all migrations",
	Run:   runMigrateReset,
}

func init() {
	rootCmd.AddCommand(migrateCmd)
	migrateCmd.AddCommand(migrateUpCmd)
	migrateCmd.AddCommand(migrateDownCmd)
	migrateCmd.AddCommand(migrateStatusCmd)
	migrateCmd.AddCommand(migrateResetCmd)
}

func getDB() *sql.DB {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	db, err := sql.Open("pgx", cfg.Database.DSN())
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	return db
}

func runMigrate(cmd *cobra.Command, args []string) {
	// Default to showing status if no subcommand
	runMigrateStatus(cmd, args)
}

func runMigrateUp(cmd *cobra.Command, args []string) {
	db := getDB()
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(migrations.Migrations)

	if err := goose.SetDialect("postgres"); err != nil {
		log.Fatalf("Failed to set dialect: %v", err)
	}

	if err := goose.Up(db, "."); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	log.Println("Migrations completed successfully")
}

func runMigrateDown(cmd *cobra.Command, args []string) {
	db := getDB()
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(migrations.Migrations)

	if err := goose.SetDialect("postgres"); err != nil {
		log.Fatalf("Failed to set dialect: %v", err)
	}

	if err := goose.Down(db, "."); err != nil {
		log.Fatalf("Failed to roll back migration: %v", err)
	}

	log.Println("Rollback completed successfully")
}

func runMigrateStatus(cmd *cobra.Command, args []string) {
	db := getDB()
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(migrations.Migrations)

	if err := goose.SetDialect("postgres"); err != nil {
		log.Fatalf("Failed to set dialect: %v", err)
	}

	if err := goose.Status(db, ".", goose.WithNoColor(false)); err != nil {
		log.Fatalf("Failed to get migration status: %v", err)
	}
}

func runMigrateReset(cmd *cobra.Command, args []string) {
	db := getDB()
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(migrations.Migrations)

	if err := goose.SetDialect("postgres"); err != nil {
		log.Fatalf("Failed to set dialect: %v", err)
	}

	if err := goose.DownTo(db, ".", 0); err != nil {
		log.Fatalf("Failed to reset database: %v", err)
	}

	log.Println("Database reset completed successfully")
}

// RunMigrationsOnStartup can be called to auto-migrate on server start
func RunMigrationsOnStartup(ctx context.Context, databaseURL string) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(migrations.Migrations)

	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}

	return goose.UpContext(ctx, db, ".")
}
