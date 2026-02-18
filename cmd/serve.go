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
	oembedapi "github.com/deicod/gotodon/internal/api/oembed"
	v1 "github.com/deicod/gotodon/internal/api/v1"
	v1alpha "github.com/deicod/gotodon/internal/api/v1alpha"
	v2 "github.com/deicod/gotodon/internal/api/v2"
	webapi "github.com/deicod/gotodon/internal/api/web"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/federation"
	"github.com/deicod/gotodon/internal/media"
	"github.com/deicod/gotodon/internal/services"
	"github.com/deicod/gotodon/internal/streaming"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/internal/workers"
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

	deliveryService := federation.NewDeliveryService()
	var (
		deliveryEnqueuer        federation.DeliveryEnqueuer
		workerManager           *workers.Manager
		remoteAccountEnqueuer   *workers.RemoteAccountEnqueuer
		remoteStatusEnqueuer    *workers.RemoteStatusEnqueuer
		scheduledStatusEnqueuer *workers.ScheduledStatusEnqueuer
		mediaProcessingEnqueuer *workers.MediaProcessingEnqueuer
		linkCrawlerEnqueuer     *workers.LinkCrawlerEnqueuer
		statusCleanupEnqueuer   *workers.StatusCleanupEnqueuer
		mediaCleanupEnqueuer    *workers.MediaCleanupEnqueuer
	)
	if cfg.Workers.Enabled && rdb != nil {
		workerManager, err = workers.NewManager(cfg, logger)
		if err != nil {
			return fmt.Errorf("initializing workers: %w", err)
		}
		deliveryEnqueuer = workers.NewDeliveryEnqueuer(workerManager.Client())
		remoteAccountEnqueuer = workers.NewRemoteAccountEnqueuer(workerManager.Client())
		remoteStatusEnqueuer = workers.NewRemoteStatusEnqueuer(workerManager.Client())
		scheduledStatusEnqueuer = workers.NewScheduledStatusEnqueuer(workerManager.Client())
		mediaProcessingEnqueuer = workers.NewMediaProcessingEnqueuer(workerManager.Client())
		linkCrawlerEnqueuer = workers.NewLinkCrawlerEnqueuer(workerManager.Client())
		statusCleanupEnqueuer = workers.NewStatusCleanupEnqueuer(workerManager.Client())
		mediaCleanupEnqueuer = workers.NewMediaCleanupEnqueuer(workerManager.Client())
	} else {
		if !cfg.Workers.Enabled {
			logger.Warn("Workers disabled by config; using in-process delivery queue")
		} else {
			logger.Warn("Workers disabled: Redis unavailable; using in-process delivery queue")
		}
		deliveryQueue := federation.NewDeliveryQueue(deliveryService.DeliverActivity, federation.DefaultDeliveryQueueConfig())
		deliveryQueue.Start()
		go func() {
			<-ctx.Done()
			_ = deliveryQueue.Stop(context.Background())
		}()
		deliveryEnqueuer = deliveryQueue
	}

	outgoingService := federation.NewOutgoingService(queries, cfg.Instance.LocalDomain, deliveryEnqueuer)

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
		mux.Handle("GET /api/v1/streaming/", pubSubHandler)
		mux.Handle("GET /api/v1/streaming/{stream...}", pubSubHandler)
	}

	// Internal handlers

	// Register v1 handlers
	accountsHandler := v1.NewAccountsHandler(queries, pool, cfg, outgoingService)
	if remoteAccountEnqueuer != nil {
		accountsHandler.SetRemoteAccountRefresher(remoteAccountEnqueuer)
	}
	accountsHandler.RegisterRoutes(mux)

	// Statuses
	statusesHandler := v1.NewStatusesHandler(queries, pool, streamingHub, cfg, outgoingService)
	statusesHandler.RegisterRoutes(mux)

	timelinesHandler := v1.NewTimelinesHandler(queries, cfg)
	timelinesHandler.RegisterRoutes(mux)

	listsHandler := v1.NewListsHandler(queries, cfg)
	listsHandler.RegisterRoutes(mux)

	pollsHandler := v1.NewPollsHandler(queries)
	pollsHandler.RegisterRoutes(mux)

	appsHandler := v1.NewAppsHandler(queries, cfg)
	appsHandler.RegisterRoutes(mux)

	notificationsHandler := v1.NewNotificationsHandler(queries, streamingHub, cfg, rdb)
	notificationsHandler.RegisterRoutes(mux)

	filtersHandler := v1.NewFiltersHandler(queries, pool)
	if streamingHub != nil {
		filtersHandler.SetPublisher(streamingHub)
	}
	filtersHandler.RegisterRoutes(mux)

	mediaStorage := media.NewLocalStorage("./uploads", fmt.Sprintf("https://%s/files", cfg.Instance.LocalDomain))
	mediaUploader := media.NewUploader(mediaStorage)
	mediaHandler := v1.NewMediaHandler(queries, mediaUploader)
	if mediaProcessingEnqueuer != nil {
		mediaHandler.SetProcessingEnqueuer(mediaProcessingEnqueuer)
	}
	mediaHandler.RegisterRoutes(mux)

	linkCrawler := services.NewLinkCrawler(queries, mediaStorage)
	statusCleanup := services.NewStatusCleanupService(queries, rdb, outgoingService, streamingHub)
	mediaCleanup := services.NewMediaCleanupService(queries, mediaStorage, logger)
	if linkCrawlerEnqueuer != nil {
		statusesHandler.SetLinkCrawler(linkCrawlerEnqueuer)
	} else {
		statusesHandler.SetLinkCrawler(&services.InProcessLinkCrawler{
			Crawler: linkCrawler,
			Logger:  logger,
		})
	}

	announcementsHandler := v1.NewAnnouncementsHandler(queries, cfg)
	if streamingHub != nil {
		announcementsHandler.SetPublisher(streamingHub)
	}
	announcementsHandler.RegisterRoutes(mux)

	markersHandler := v1.NewMarkersHandler(queries)
	markersHandler.RegisterRoutes(mux)

	v1AlphaAsyncRefreshHandler := v1alpha.NewAsyncRefreshHandler(cfg, v1alpha.NewRedisAsyncRefreshStore(rdb))
	v1AlphaAsyncRefreshHandler.RegisterRoutes(mux)

	preferencesHandler := v1.NewPreferencesHandler(queries)
	preferencesHandler.RegisterRoutes(mux)

	suggestionsHandler := v1.NewSuggestionsHandler(queries, cfg)
	suggestionsHandler.RegisterRoutes(mux)

	trendsHandler := v1.NewTrendsHandler(queries, cfg)
	trendsHandler.RegisterRoutes(mux)

	directoryHandler := v1.NewDirectoryHandler(queries, cfg)
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

	followRequestsHandler := v1.NewFollowRequestsHandler(queries, cfg, outgoingService)
	followRequestsHandler.RegisterRoutes(mux)

	domainBlocksHandler := v1.NewDomainBlocksHandler(queries, cfg)
	domainBlocksHandler.RegisterRoutes(mux)

	scheduledStatusesHandler := v1.NewScheduledStatusesHandler(queries)
	scheduledStatusesHandler.RegisterRoutes(mux)

	v1PushHandler := v1.NewPushHandler(queries, cfg)
	v1PushHandler.RegisterRoutes(mux)

	v1EndorsementsHandler := v1.NewEndorsementsHandler(queries, cfg)
	v1EndorsementsHandler.RegisterRoutes(mux)

	v1FeaturedTagsHandler := v1.NewFeaturedTagsHandler(queries, cfg)
	v1FeaturedTagsHandler.RegisterRoutes(mux)

	v1TagsHandler := v1.NewTagsHandler(queries, cfg)
	v1TagsHandler.RegisterRoutes(mux)

	v1InstanceHandler := v1.NewInstanceHandler(queries, cfg)
	v1InstanceHandler.RegisterRoutes(mux)

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
	v2InstanceHandler := v2.NewInstanceHandler(queries, cfg)
	v2InstanceHandler.RegisterRoutes(mux)

	// Filters
	v2FiltersHandler := v2.NewFiltersHandler(queries)
	if streamingHub != nil {
		v2FiltersHandler.SetPublisher(streamingHub)
	}
	v2FiltersHandler.RegisterRoutes(mux)

	searchHandler := v2.NewSearchHandler(queries, cfg)
	searchHandler.RegisterRoutes(mux)

	v2SuggestionsHandler := v2.NewSuggestionsHandler(queries, cfg)
	v2SuggestionsHandler.RegisterRoutes(mux)

	v2NotificationsHandler := v2.NewNotificationsHandler(queries, cfg)
	v2NotificationsHandler.RegisterRoutes(mux)

	v2AdminAccountsHandler := v2.NewAdminAccountsHandler(queries, cfg)
	v2AdminAccountsHandler.RegisterRoutes(mux)

	// Register web handlers
	webEmbedsHandler := webapi.NewEmbedsHandler(queries, cfg)
	webEmbedsHandler.RegisterRoutes(mux)

	webSettingsHandler := webapi.NewSettingsHandler(queries)
	webSettingsHandler.RegisterRoutes(mux)

	webPushSubscriptionsHandler := webapi.NewPushSubscriptionsHandler(queries, cfg)
	webPushSubscriptionsHandler.RegisterRoutes(mux)

	// Register OAuth handlers
	oauthHandler := oauthapi.NewHandler(queries, cfg)
	oauthHandler.RegisterRoutes(mux)

	oembedHandler := oembedapi.NewHandler(queries, cfg)
	oembedHandler.RegisterRoutes(mux)

	// ActivityPub Federation
	federationHandler := activitypubapi.NewFederationHandler(queries, cfg.Instance.LocalDomain)
	followService := federation.NewFollowService(queries, cfg.Instance.LocalDomain, federationHandler.ActorFetcher(), nil)
	followResponseService := federation.NewFollowResponseService(queries, cfg.Instance.LocalDomain)
	undoService := federation.NewUndoService(queries, cfg.Instance.LocalDomain)
	createService := federation.NewCreateService(queries, cfg.Instance.LocalDomain, federationHandler.ActorFetcher())
	remoteStatusFetcher := federation.NewRemoteStatusFetcher(queries, cfg.Instance.LocalDomain, federationHandler.ActorFetcher())
	createService.SetStatusFetcher(remoteStatusFetcher)
	if remoteStatusEnqueuer != nil {
		statusesHandler.SetThreadFetcher(remoteStatusEnqueuer)
	} else {
		statusesHandler.SetThreadFetcher(remoteStatusFetcher)
	}
	announceService := federation.NewAnnounceService(queries, cfg.Instance.LocalDomain, federationHandler.ActorFetcher(), nil)
	likeService := federation.NewLikeService(queries, cfg.Instance.LocalDomain, federationHandler.ActorFetcher())
	blockService := federation.NewBlockService(queries, cfg.Instance.LocalDomain, federationHandler.ActorFetcher())
	updateService := federation.NewUpdateService(queries, cfg.Instance.LocalDomain, federationHandler.ActorFetcher())
	deleteService := federation.NewDeleteService(queries, cfg.Instance.LocalDomain)
	federationHandler.RegisterInboxHandler(activitypub.TypeFollow, followService.HandleFollow)
	federationHandler.RegisterInboxHandler(activitypub.TypeAccept, followResponseService.HandleAccept)
	federationHandler.RegisterInboxHandler(activitypub.TypeReject, followResponseService.HandleReject)
	federationHandler.RegisterInboxHandler(activitypub.TypeUndo, undoService.HandleUndo)
	federationHandler.RegisterInboxHandler(activitypub.TypeCreate, createService.HandleCreate)
	federationHandler.RegisterInboxHandler(activitypub.TypeAnnounce, announceService.HandleAnnounce)
	federationHandler.RegisterInboxHandler(activitypub.TypeLike, likeService.HandleLike)
	federationHandler.RegisterInboxHandler(activitypub.TypeBlock, blockService.HandleBlock)
	federationHandler.RegisterInboxHandler(activitypub.TypeUpdate, updateService.HandleUpdate)
	federationHandler.RegisterInboxHandler(activitypub.TypeDelete, deleteService.HandleDelete)
	federationHandler.RegisterRoutes(mux)

	if workerManager != nil {
		remoteAccountResolver := federation.NewRemoteActorResolver(queries, cfg.Instance.LocalDomain)
		if err := workerManager.Register(
			&workers.DeliveryRegistrar{Store: queries, Deliver: deliveryService.DeliverActivity},
			&workers.RemoteAccountRegistrar{Resolver: remoteAccountResolver},
			&workers.RemoteStatusRegistrar{Fetcher: remoteStatusFetcher, Lookup: queries},
			&workers.ScheduledStatusRegistrar{Store: queries, Publisher: statusesHandler},
			&workers.MediaProcessingRegistrar{Store: queries, Storage: mediaStorage},
			&workers.LinkCrawlerRegistrar{Crawler: linkCrawler},
			&workers.StatusCleanupRegistrar{Runner: statusCleanup},
			&workers.MediaCleanupRegistrar{Runner: mediaCleanup},
		); err != nil {
			return fmt.Errorf("registering workers: %w", err)
		}
		startErr := make(chan error, 1)
		go func() {
			startErr <- workerManager.Start()
		}()
		select {
		case err := <-startErr:
			if err != nil {
				return fmt.Errorf("starting workers: %w", err)
			}
		case <-time.After(250 * time.Millisecond):
			go func() {
				if err := <-startErr; err != nil {
					logger.Error("Worker server stopped", "error", err)
					cancel()
				}
			}()
		}
		if scheduledStatusEnqueuer != nil {
			go workers.StartScheduledStatusPolling(ctx, scheduledStatusEnqueuer, 0)
		}
		if statusCleanupEnqueuer != nil {
			go workers.StartStatusCleanupPolling(ctx, statusCleanupEnqueuer, 0)
		}
		if mediaCleanupEnqueuer != nil {
			go workers.StartMediaCleanupPolling(ctx, mediaCleanupEnqueuer, 0)
		}
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := workerManager.Shutdown(shutdownCtx); err != nil {
				logger.Error("Failed to stop workers", "error", err)
			}
		}()
	}

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

	adminCanonicalEmailBlocksHandler := admin.NewCanonicalEmailBlocksHandler(queries)
	adminCanonicalEmailBlocksHandler.RegisterRoutes(mux)

	adminEmailDomainBlocksHandler := admin.NewEmailDomainBlocksHandler(queries, rdb)
	adminEmailDomainBlocksHandler.RegisterRoutes(mux)

	adminTagsHandler := admin.NewTagsHandler(queries)
	adminTagsHandler.RegisterRoutes(mux)

	adminReportsHandler := admin.NewReportsHandler(queries)
	adminReportsHandler.RegisterRoutes(mux)

	adminInstanceHandler := admin.NewInstanceHandler(queries, cfg)
	adminInstanceHandler.RegisterRoutes(mux)

	adminTrendsHandler := admin.NewTrendsHandler(queries, cfg)
	adminTrendsHandler.RegisterRoutes(mux)

	adminDimensionsHandler := admin.NewDimensionsHandler(queries, cfg, rdb)
	adminDimensionsHandler.RegisterRoutes(mux)

	adminMeasuresHandler := admin.NewMeasuresHandler(queries)
	adminMeasuresHandler.RegisterRoutes(mux)

	adminRetentionHandler := admin.NewRetentionHandler(queries)
	adminRetentionHandler.RegisterRoutes(mux)

	adminCustomEmojisHandler := admin.NewCustomEmojisHandler(queries, mediaUploader)
	adminCustomEmojisHandler.RegisterRoutes(mux)

	// V1 Reports (User API)
	v1ReportsHandler := v1.NewReportsHandler(queries)
	mux.HandleFunc("POST /api/v1/reports", v1ReportsHandler.Create)

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
	tokenValidator := oauthapi.NewTokenValidator(queries)

	handler := middleware.Recover(logger)(
		middleware.Logger(logger)(
			middleware.CORS(
				middleware.Authenticate(tokenValidator)(mux),
			),
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
