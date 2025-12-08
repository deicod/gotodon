/*
Copyright © 2025 Darko Luketic <info@icod.de>
*/
package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/spf13/cobra"

	v1 "github.com/deicod/gotodon/internal/api/v1"
	v2 "github.com/deicod/gotodon/internal/api/v2"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

// serveCmd represents the serve command
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the Gotodon server",
	Long: `Start the Gotodon ActivityPub server.

This command starts the HTTP server that handles the Mastodon-compatible
REST API, ActivityPub federation, and streaming endpoints.`,
	RunE: runServer,
}

func init() {
	rootCmd.AddCommand(serveCmd)

	serveCmd.Flags().StringP("config", "c", "", "config file path")
	serveCmd.Flags().IntP("port", "p", 0, "HTTP port (overrides config)")
}

func runServer(cmd *cobra.Command, _ []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Override port if specified
	if port, _ := cmd.Flags().GetInt("port"); port != 0 {
		cfg.Server.Port = port
	}

	// Setup logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Connect to PostgreSQL (required)
	if cfg.Database.Host == "" {
		return fmt.Errorf("database configuration required: set DB_HOST or database.host")
	}

	pool, err := pgxpool.New(ctx, cfg.Database.DSN())
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()

	// Verify connection
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("database ping failed: %w", err)
	}

	queries := db.New(pool)
	logger.Info("Connected to PostgreSQL database")

	// Create Echo instance
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	// Middleware
	e.Use(middleware.RequestID())
	e.Use(middleware.Recover())
	e.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{
		Format: `{"time":"${time_rfc3339}","id":"${id}","method":"${method}","uri":"${uri}","status":${status},"latency":"${latency_human}"}` + "\n",
	}))
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowHeaders: []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAccept, echo.HeaderAuthorization},
	}))

	// Health check
	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// API groups
	api := e.Group("/api")
	v1Group := api.Group("/v1")
	v2Group := api.Group("/v2")

	// Register v1 handlers
	listsHandler := v1.NewListsHandler(queries)
	listsHandler.RegisterRoutes(v1Group)

	pollsHandler := v1.NewPollsHandler(queries)
	pollsHandler.RegisterRoutes(v1Group)

	// Register v2 handlers
	filtersHandler := v2.NewFiltersHandler(queries)
	filtersHandler.RegisterRoutes(v2Group)

	// Instance info (public endpoint)
	v1Group.GET("/instance", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"uri":               cfg.Instance.LocalDomain,
			"title":             "Gotodon",
			"short_description": "A Mastodon-compatible server written in Go",
			"description":       "A Mastodon-compatible ActivityPub server written in Go",
			"email":             "",
			"version":           "4.5.2 (compatible; Gotodon 0.1.0)",
			"urls": map[string]string{
				"streaming_api": fmt.Sprintf("wss://%s", cfg.Instance.LocalDomain),
			},
			"stats": map[string]int{
				"user_count":   0,
				"status_count": 0,
				"domain_count": 0,
			},
			"registrations":     cfg.Instance.RegistrationsMode == "open",
			"approval_required": cfg.Instance.RegistrationsMode == "approved",
			"invites_enabled":   false,
			"configuration": map[string]interface{}{
				"statuses": map[string]int{
					"max_characters":              500,
					"max_media_attachments":       4,
					"characters_reserved_per_url": 23,
				},
				"media_attachments": map[string]interface{}{
					"supported_mime_types": []string{
						"image/jpeg", "image/png", "image/gif", "image/webp",
						"video/webm", "video/mp4", "video/quicktime",
						"audio/mpeg", "audio/ogg", "audio/flac", "audio/wav",
					},
					"image_size_limit":       16777216,
					"image_matrix_limit":     33177600,
					"video_size_limit":       103809024,
					"video_frame_rate_limit": 120,
					"video_matrix_limit":     8294400,
				},
				"polls": map[string]int{
					"max_options":               4,
					"max_characters_per_option": 50,
					"min_expiration":            300,
					"max_expiration":            2629746,
				},
			},
			"contact_account": nil,
			"rules":           []interface{}{},
		})
	})

	// Apps endpoint (for OAuth app registration)
	v1Group.POST("/apps", func(c echo.Context) error {
		// TODO: Implement app registration
		return c.JSON(http.StatusNotImplemented, map[string]string{
			"error": "Not implemented yet",
		})
	})

	// Well-known endpoints
	wellKnown := e.Group("/.well-known")
	wellKnown.GET("/webfinger", func(c echo.Context) error {
		// TODO: Implement WebFinger
		return c.JSON(http.StatusNotImplemented, map[string]string{
			"error": "Not implemented yet",
		})
	})
	wellKnown.GET("/nodeinfo", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"links": []map[string]string{
				{
					"rel":  "http://nodeinfo.diaspora.software/ns/schema/2.0",
					"href": fmt.Sprintf("https://%s/nodeinfo/2.0", cfg.Instance.LocalDomain),
				},
			},
		})
	})

	// NodeInfo
	e.GET("/nodeinfo/2.0", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"version": "2.0",
			"software": map[string]string{
				"name":    "gotodon",
				"version": "0.1.0",
			},
			"protocols": []string{"activitypub"},
			"usage": map[string]interface{}{
				"users": map[string]int{
					"total":          0,
					"activeMonth":    0,
					"activeHalfyear": 0,
				},
				"localPosts": 0,
			},
			"openRegistrations": cfg.Instance.RegistrationsMode == "open",
		})
	})

	// Start server
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	logger.Info("Starting Gotodon server", "addr", addr, "domain", cfg.Instance.LocalDomain)

	go func() {
		if err := e.Start(addr); err != nil && err != http.ErrServerClosed {
			logger.Error("Server error", "error", err)
		}
	}()

	// Wait for interrupt signal
	<-ctx.Done()
	logger.Info("Shutting down server...")

	// Graceful shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := e.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server shutdown: %w", err)
	}

	logger.Info("Server stopped")
	return nil
}
