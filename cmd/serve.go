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
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"

	activitypubapi "github.com/deicod/gotodon/internal/api/activitypub"
	"github.com/deicod/gotodon/internal/api/admin"
	"github.com/deicod/gotodon/internal/api/middleware"
	oauthapi "github.com/deicod/gotodon/internal/api/oauth"
	v1 "github.com/deicod/gotodon/internal/api/v1"
	v2 "github.com/deicod/gotodon/internal/api/v2"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/media"
	"github.com/deicod/gotodon/internal/streaming"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/activitypub"
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

	// Connect to Redis (required for streaming)
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr(),
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.Database,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		logger.Warn("Failed to connect to Redis, streaming will be disabled", "error", err)
		rdb = nil
	} else {
		logger.Info("Connected to Redis", "addr", cfg.Redis.Addr())
	}

	// Create ServeMux
	mux := http.NewServeMux()

	// Streaming API (Initialize before handlers to inject)
	var streamingHub *streaming.RedisHub
	if rdb != nil {
		streamingHub = streaming.NewRedisHub(rdb, "gotodon")
		if err := streamingHub.Start(context.Background()); err != nil {
			logger.Error("Failed to start RedisHub", "error", err)
		} else {
			go func() {
				<-ctx.Done()
				_ = streamingHub.Stop()
			}()
		}
	} else {
		logger.Warn("Redis unavailable, streaming API will not function correctly across instances")
	}

	var pubSubHandler http.Handler
	if streamingHub != nil {
		wsHandler := streaming.NewWSHandler(streamingHub.Hub)
		sseHandler := streaming.NewSSEHandler(streamingHub.Hub)

		pubSubHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Upgrade") == "websocket" {
				wsHandler.ServeHTTP(w, r)
			} else {
				sseHandler.ServeHTTP(w, r)
			}
		})

		mux.Handle("GET /api/v1/streaming", pubSubHandler)
	}

	// Internal handlers

	// Register v1 handlers
	accountsHandler := v1.NewAccountsHandler(queries, pool, cfg)
	accountsHandler.RegisterRoutes(mux)

	// Statuses
	statusesHandler := v1.NewStatusesHandler(queries, pool, streamingHub, cfg)
	statusesHandler.RegisterRoutes(mux)

	timelinesHandler := v1.NewTimelinesHandler(queries, cfg)
	timelinesHandler.RegisterRoutes(mux)

	listsHandler := v1.NewListsHandler(queries)
	listsHandler.RegisterRoutes(mux)

	pollsHandler := v1.NewPollsHandler(queries)
	pollsHandler.RegisterRoutes(mux)

	appsHandler := v1.NewAppsHandler(queries)
	appsHandler.RegisterRoutes(mux)

	notificationsHandler := v1.NewNotificationsHandler(queries, streamingHub)
	notificationsHandler.RegisterRoutes(mux)

	mediaStorage := media.NewLocalStorage("./uploads", fmt.Sprintf("https://%s/files", cfg.Instance.LocalDomain))
	mediaUploader := media.NewUploader(mediaStorage)
	mediaHandler := v1.NewMediaHandler(queries, mediaUploader)
	mediaHandler.RegisterRoutes(mux)

	announcementsHandler := v1.NewAnnouncementsHandler(queries)
	announcementsHandler.RegisterRoutes(mux)

	markersHandler := v1.NewMarkersHandler(queries)
	markersHandler.RegisterRoutes(mux)

	preferencesHandler := v1.NewPreferencesHandler(queries)
	preferencesHandler.RegisterRoutes(mux)

	suggestionsHandler := v1.NewSuggestionsHandler(queries)
	suggestionsHandler.RegisterRoutes(mux)

	trendsHandler := v1.NewTrendsHandler(queries)
	trendsHandler.RegisterRoutes(mux)

	directoryHandler := v1.NewDirectoryHandler(queries)
	directoryHandler.RegisterRoutes(mux)

	bookmarksHandler := v1.NewBookmarksHandler(queries, cfg)
	bookmarksHandler.RegisterRoutes(mux)

	favouritesHandler := v1.NewFavouritesHandler(queries, cfg)
	favouritesHandler.RegisterRoutes(mux)

	mutesHandler := v1.NewMutesHandler(queries, cfg)
	mutesHandler.RegisterRoutes(mux)

	blocksHandler := v1.NewBlocksHandler(queries, cfg)
	blocksHandler.RegisterRoutes(mux)

	customEmojisHandler := v1.NewCustomEmojisHandler(queries)
	customEmojisHandler.RegisterRoutes(mux)

	conversationsHandler := v1.NewConversationsHandler(queries, cfg)
	conversationsHandler.RegisterRoutes(mux)

	followRequestsHandler := v1.NewFollowRequestsHandler(queries, cfg)
	followRequestsHandler.RegisterRoutes(mux)

	domainBlocksHandler := v1.NewDomainBlocksHandler(queries, cfg)
	domainBlocksHandler.RegisterRoutes(mux)

	scheduledStatusesHandler := v1.NewScheduledStatusesHandler(queries)
	scheduledStatusesHandler.RegisterRoutes(mux)

	v1PushHandler := v1.NewPushHandler(queries, cfg)
	v1PushHandler.RegisterRoutes(mux)

	v1EndorsementsHandler := v1.NewEndorsementsHandler(queries)
	v1EndorsementsHandler.RegisterRoutes(mux)

	v1FeaturedTagsHandler := v1.NewFeaturedTagsHandler(queries)
	v1FeaturedTagsHandler.RegisterRoutes(mux)

	v1TagsHandler := v1.NewTagsHandler(queries, cfg)
	v1TagsHandler.RegisterRoutes(mux)

	v1InstanceInfoHandler := v1.NewInstanceInfoHandler(queries, cfg)
	v1InstanceInfoHandler.RegisterRoutes(mux)

	v1ProfileHandler := v1.NewProfileHandler(queries, cfg)
	v1ProfileHandler.RegisterRoutes(mux)

	v1AnnualReportsHandler := v1.NewAnnualReportsHandler(queries)
	v1AnnualReportsHandler.RegisterRoutes(mux)

	v1EmailsHandler := v1.NewEmailsHandler(queries)
	v1EmailsHandler.RegisterRoutes(mux)

	v1PeersHandler := v1.NewPeersHandler(queries)
	v1PeersHandler.RegisterRoutes(mux)

	// Register v2 handlers
	// V2 Generic
	v2InstanceHandler := v2.NewInstanceHandler(cfg)
	v2InstanceHandler.RegisterRoutes(mux)

	// Filters
	v2FiltersHandler := v2.NewFiltersHandler(queries)
	v2FiltersHandler.RegisterRoutes(mux)

	searchHandler := v2.NewSearchHandler(queries)
	searchHandler.RegisterRoutes(mux)

	v2SuggestionsHandler := v2.NewSuggestionsHandler(queries)
	v2SuggestionsHandler.RegisterRoutes(mux)

	v2NotificationsHandler := v2.NewNotificationsHandler(queries, cfg)
	v2NotificationsHandler.RegisterRoutes(mux)

	// Register OAuth handlers
	oauthHandler := oauthapi.NewHandler(queries)
	oauthHandler.RegisterRoutes(mux)

	// ActivityPub Federation
	federationHandler := activitypubapi.NewFederationHandler(queries, cfg.Instance.LocalDomain)
	federationHandler.RegisterRoutes(mux)

	// Admin API
	// Admin Handlers
	adminAccountsHandler := admin.NewAccountsHandler(queries)
	adminAccountsHandler.RegisterRoutes(mux)

	adminDomainAllowsHandler := admin.NewDomainAllowsHandler(queries)
	adminDomainAllowsHandler.RegisterRoutes(mux)

	adminDomainBlocksHandler := admin.NewDomainBlocksHandler(queries)
	adminDomainBlocksHandler.RegisterRoutes(mux)

	adminIpBlocksHandler := admin.NewIpBlocksHandler(queries)
	adminIpBlocksHandler.RegisterRoutes(mux)

	adminReportsHandler := admin.NewReportsHandler(queries)
	adminReportsHandler.RegisterRoutes(mux)

	adminInstanceHandler := admin.NewInstanceHandler()
	adminInstanceHandler.RegisterRoutes(mux)

	adminTrendsHandler := admin.NewTrendsHandler()
	adminTrendsHandler.RegisterRoutes(mux)

	adminDimensionsHandler := admin.NewDimensionsHandler()
	adminDimensionsHandler.RegisterRoutes(mux)

	adminMeasuresHandler := admin.NewMeasuresHandler()
	adminMeasuresHandler.RegisterRoutes(mux)

	adminRetentionHandler := admin.NewRetentionHandler()
	adminRetentionHandler.RegisterRoutes(mux)

	adminCustomEmojisHandler := admin.NewCustomEmojisHandler(queries, mediaUploader)
	adminCustomEmojisHandler.RegisterRoutes(mux)

	// V1 Reports (User API)
	v1ReportsHandler := v1.NewReportsHandler(queries)
	mux.HandleFunc("POST /api/v1/reports", v1ReportsHandler.Create)

	// Instance info (public endpoint)
	mux.HandleFunc("GET /api/v1/instance", func(w http.ResponseWriter, r *http.Request) {
		web.JSON(w, http.StatusOK, map[string]interface{}{
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
	v1AppsHandler := v1.NewAppsHandler(queries)
	v1AppsHandler.RegisterRoutes(mux)

	// Well-known endpoints
	mux.HandleFunc("GET /.well-known/webfinger", func(w http.ResponseWriter, r *http.Request) {
		resource := web.QueryParam(r, "resource")
		if resource == "" {
			web.JSON(w, http.StatusBadRequest, map[string]string{
				"error": "Missing required 'resource' parameter",
			})
			return
		}

		username, domain, err := activitypub.ParseAcct(resource)
		if err != nil {
			web.JSON(w, http.StatusBadRequest, map[string]string{
				"error": "Invalid resource format. Expected acct:username@domain",
			})
			return
		}

		// Only handle requests for local domain
		if domain != cfg.Instance.LocalDomain {
			web.JSON(w, http.StatusNotFound, map[string]string{
				"error": "User not found on this server",
			})
			return
		}

		account, err := queries.GetAccountByUsername(r.Context(), username)
		if err != nil {
			web.JSON(w, http.StatusNotFound, map[string]string{
				"error": "Account not found",
			})
			return
		}

		// Build WebFinger response
		accountURI := fmt.Sprintf("https://%s/users/%s", cfg.Instance.LocalDomain, account.Username)
		profileURL := fmt.Sprintf("https://%s/@%s", cfg.Instance.LocalDomain, account.Username)

		resp := activitypub.BuildWebFingerResponse(account.Username, cfg.Instance.LocalDomain, accountURI, profileURL)

		w.Header().Set("Content-Type", activitypub.WebFingerContentType)
		web.JSON(w, http.StatusOK, resp)
	})

	mux.HandleFunc("GET /.well-known/host-meta", func(w http.ResponseWriter, r *http.Request) {
		// Return XRD host-meta as per RFC 6415
		xml := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<XRD xmlns="http://docs.oasis-open.org/ns/xri/xrd-1.0">
  <Link rel="lrdd" template="https://%s/.well-known/webfinger?resource={uri}"/>
</XRD>`, cfg.Instance.LocalDomain)
		w.Header().Set("Content-Type", "application/xrd+xml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(xml)); err != nil {
			logger.Error("Failed to write host-meta XML response", "error", err)
		}
	})

	mux.HandleFunc("GET /.well-known/nodeinfo", func(w http.ResponseWriter, r *http.Request) {
		web.JSON(w, http.StatusOK, map[string]interface{}{
			"links": []map[string]string{
				{
					"rel":  "http://nodeinfo.diaspora.software/ns/schema/2.0",
					"href": fmt.Sprintf("https://%s/nodeinfo/2.0", cfg.Instance.LocalDomain),
				},
			},
		})
	})

	// NodeInfo
	mux.HandleFunc("GET /nodeinfo/2.0", func(w http.ResponseWriter, r *http.Request) {
		web.JSON(w, http.StatusOK, map[string]interface{}{
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

	// Health check
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		web.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Setup middleware chain
	handler := middleware.Recover(logger)(
		middleware.Logger(logger)(
			middleware.CORS(mux),
		),
	)

	// Start server
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	logger.Info("Starting Gotodon server", "addr", addr, "domain", cfg.Instance.LocalDomain)

	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server error", "error", err)
		}
	}()

	// Wait for interrupt signal
	<-ctx.Done()
	logger.Info("Shutting down server...")

	// Graceful shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server shutdown: %w", err)
	}

	logger.Info("Server stopped")
	return nil
}
