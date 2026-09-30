// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
	"github.com/swaggest/swgui/v5emb"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/auth"
	"github.com/torrplay/torrplay/internal/buildinfo"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/dlna"
	"github.com/torrplay/torrplay/internal/downloader"
	"github.com/torrplay/torrplay/internal/httpclient"
	"github.com/torrplay/torrplay/internal/httpserver"
	"github.com/torrplay/torrplay/internal/images"
	"github.com/torrplay/torrplay/internal/logging"
	"github.com/torrplay/torrplay/internal/metrics"
	"github.com/torrplay/torrplay/internal/piececompletion"
	"github.com/torrplay/torrplay/internal/settings"
	"github.com/torrplay/torrplay/internal/utils"
	memstorage "github.com/torrplay/torrplay/pkg/storage"
	"github.com/torrplay/torrplay/pkg/stream"
	"github.com/torrplay/torrplay/web"
	"golang.org/x/time/rate"
	"gopkg.in/natefinch/lumberjack.v2"
)

const (
	fileStorageReadahead = 50 * 1024 * 1024 // 50MB
	// a threshold to prevent the "viewed" timestamp for a file from being updated too frequently.
	fileViewedUpdateThreshold = 5 * time.Minute
	gotInfoTimeoutMsg         = "timeout waiting for torrent metadata"
	imageDownloadTimeout      = 1 * time.Minute
	multipartFormMaxBody      = 32 << 20
	multipartFormMaxMemory    = 1 << 20
	posterCleanupInterval     = 6 * time.Hour
	profilerAddress           = "127.0.0.1:6060"
	profilerShutdownTimeout   = 5 * time.Second
	torrentTrackerTTL         = 3 * time.Hour
)

var _ api.ServerInterface = (*Controller)(nil)

// controllerRuntimeConfig contains private runtime dependencies and timing knobs
// used to keep controller tests deterministic without expanding the public API.
type controllerRuntimeConfig struct {
	clientCloseDelay time.Duration
	configureClient  func(*torrent.ClientConfig)
	fetchTrackers    func(context.Context, *httpclient.Client) ([][]string, error)
	gotInfoTimeout   time.Duration
}

func defaultControllerRuntimeConfig() controllerRuntimeConfig {
	return controllerRuntimeConfig{
		clientCloseDelay: 500 * time.Millisecond,
		fetchTrackers:    utils.FetchTrackers,
		gotInfoTimeout:   30 * time.Second,
	}
}

type torrentInfo struct {
	lastUsedAt  time.Time
	storageType api.TorrentStorage
}

// torrentTracker tracks loaded torrents and drops those that have been inactive for a specified time-to-live (TTL).
// This prevents the system from being overloaded with unused torrents and automatically frees up their associated memory.
type torrentTracker struct {
	cleanupDone   chan struct{}
	cleanupTicker *time.Ticker
	mu            sync.RWMutex
	torrents      map[metainfo.Hash]torrentInfo
	ttl           time.Duration
}

type Controller struct {
	client               *torrent.Client
	dataDir              string
	db                   database.DatabaseInterface
	dlna                 *dlna.Service
	dlnaPath             string
	downloader           atomic.Pointer[downloader.Downloader]
	httpAddr             string
	httpClient           *httpclient.Client
	httpServer           *httpserver.Server
	images               images.ServiceInterface
	lastMetricsTime      time.Time
	lastTotalDownload    int64
	lastTotalUpload      int64
	logFile              io.Closer
	logger               atomic.Pointer[slog.Logger]
	metrics              *metrics.Metrics
	metricsMu            sync.RWMutex
	mu                   sync.RWMutex
	pieceCompletion      piececompletion.DeletablePieceCompletion
	port                 int
	posterCleanupDone    chan struct{}
	posterCleanupTicker  *time.Ticker
	posterOpMu           sync.Mutex
	posterWorkers        sync.WaitGroup
	posterWorkersMu      sync.Mutex
	posterWorkersStopped bool
	postersPath          string
	profilerAddr         string
	profilerListener     net.Listener
	profilerMu           sync.Mutex
	profilerServer       *http.Server
	router               *chi.Mux
	runtimeConfig        controllerRuntimeConfig
	settings             atomic.Pointer[api.Settings]
	// settingsUpdateMu serializes settings updates from read through apply.
	settingsUpdateMu sync.Mutex
	// torrentClientUnavailable is set while the torrent client, storage, and
	// stream pool are being replaced, and stays set if the replacement fails.
	// Streams and preloads are rejected while it is set.
	torrentClientUnavailable atomic.Bool
	torrentConfigMu          sync.Mutex
	torrentGeneration        atomic.Uint64
	startedAt                time.Time
	storageClient            atomic.Pointer[memstorage.Client]
	streamPool               atomic.Pointer[stream.Pool]
	torrentTracker           torrentTracker
	trackers                 [][]string

	api.Unimplemented
}

func NewController(dataDir string, ipAddr string, port int, dbClient database.DatabaseInterface, imgService images.ServiceInterface, metricsRegistry *metrics.Metrics) (*Controller, error) {
	return newController(dataDir, ipAddr, port, dbClient, imgService, metricsRegistry, defaultControllerRuntimeConfig())
}

func newController(dataDir string, ipAddr string, port int, dbClient database.DatabaseInterface, imgService images.ServiceInterface, metricsRegistry *metrics.Metrics, runtimeConfig controllerRuntimeConfig) (*Controller, error) {
	defaults := defaultControllerRuntimeConfig()
	if runtimeConfig.fetchTrackers == nil {
		runtimeConfig.fetchTrackers = defaults.fetchTrackers
	}
	if runtimeConfig.gotInfoTimeout <= 0 {
		runtimeConfig.gotInfoTimeout = defaults.gotInfoTimeout
	}
	// A zero close delay intentionally disables the production settling delay.
	if runtimeConfig.clientCloseDelay < 0 {
		runtimeConfig.clientCloseDelay = defaults.clientCloseDelay
	}

	dbSettings, err := dbClient.GetSettings()
	if err != nil {
		if errors.Is(err, database.ErrSettingsNotFound) {
			def := settings.Default()
			dbSettings = database.FromAPISettings(&def)
			if err := dbClient.UpdateSettings(dbSettings); err != nil {
				return nil, fmt.Errorf("failed to save default settings: %w", err)
			}
		} else {
			return nil, fmt.Errorf("failed to get settings: %w", err)
		}
	}

	appSettings := database.ToAPISettings(dbSettings)
	if settings.Merge(appSettings, settings.Default()) {
		if err := dbClient.UpdateSettings(database.FromAPISettings(appSettings)); err != nil {
			return nil, fmt.Errorf("failed to update default settings: %w", err)
		}
	}
	logging.DefaultStore.Resize(utils.Val(appSettings.LogStoreSize))

	c := &Controller{
		dataDir:           dataDir,
		runtimeConfig:     runtimeConfig,
		db:                dbClient,
		dlnaPath:          "/upnp/",
		images:            imgService,
		httpAddr:          ipAddr,
		httpClient:        httpclient.New(),
		metrics:           metricsRegistry,
		port:              port,
		posterCleanupDone: make(chan struct{}),
		postersPath:       "/posters/",
		profilerAddr:      profilerAddress,
		startedAt:         time.Now(),
		torrentTracker: torrentTracker{
			cleanupDone: make(chan struct{}),
			torrents:    make(map[metainfo.Hash]torrentInfo),
			ttl:         torrentTrackerTTL,
		},
	}

	c.settings.Store(appSettings)
	c.logger.Store(c.configureLogger(*appSettings.LogLevel, appSettings))

	var pc piececompletion.DeletablePieceCompletion
	if fsp := utils.Val(appSettings.FileStoragePath); fsp != "" {
		if _, err := os.Stat(fsp); os.IsNotExist(err) {
			if err := os.MkdirAll(fsp, 0o755); err != nil {
				return nil, fmt.Errorf("failed to create file storage directory: %w", err)
			}
		}
		pc, err = piececompletion.New(fsp, c.logger.Load())
		if err != nil {
			return nil, fmt.Errorf("failed to create piece completion database: %w", err)
		}
	}
	c.pieceCompletion = pc

	c.dlna = dlna.NewService(dbClient, c.dlnaPath, c.postersPath, c.logger.Load())

	// Check for auth override environment variable. This allows a user to regain
	// access to their settings if they have forgotten their credentials.
	if authOverride, exists := os.LookupEnv("TORRPLAY_DISABLE_AUTH"); exists {
		if enabled, err := strconv.ParseBool(authOverride); err == nil && enabled {
			if appSettings.Auth == nil {
				appSettings.Auth = &api.Auth{}
			}
			appSettings.Auth.Enabled = new(false)
			c.logger.Load().Warn("Authentication has been disabled via the TORRPLAY_DISABLE_AUTH environment variable.")
		}
	}

	c.logger.Load().Info("initializing TorrPlay with data directory: " + c.dataDir)

	if appSettings.TorrentTrackers != nil && len(*appSettings.TorrentTrackers) > 0 {
		for _, tracker := range *appSettings.TorrentTrackers {
			c.trackers = append(c.trackers, strings.Split(tracker, ","))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	trackers, err := c.runtimeConfig.fetchTrackers(ctx, c.httpClient)
	if err != nil {
		c.logger.Load().Debug(fmt.Sprintf("failed to get trackers, %v", err.Error()))
	}
	c.trackers = append(c.trackers, trackers...)

	err = c.configureTorrentClient()
	if err != nil {
		return nil, err
	}

	c.downloader.Store(downloader.New(c.client, c.db, c.logger.Load(), c.metrics, c.pieceCompletion, utils.Val(c.settings.Load().FileStoragePath), c.trackers))
	if *c.settings.Load().EnableDlna {
		err = c.dlna.Start(*c.settings.Load().FriendlyName, c.httpAddr, c.resolveHTTPPort())
		if err != nil {
			return nil, err
		}
	}

	if *appSettings.EnableDownloader {
		c.downloader.Load().Start()
	}

	go c.startTorrentCleanup()
	c.startPosterWorker(c.startPosterCleanup)

	return c, nil
}

func (c *Controller) Logger() *slog.Logger {
	return c.logger.Load()
}

func (c *Controller) Settings() *api.Settings {
	return c.settings.Load()
}

func (c *Controller) buildRouter() *chi.Mux {
	swagger, err := api.GetSwagger()
	if err != nil {
		panic(fmt.Sprintf("failed to load swagger spec: %v", err))
	}
	swagger.Servers = nil

	router := chi.NewRouter()
	router.Use(c.SlogMiddleware())
	router.Use(c.MetricsMiddleware())
	router.Use(middleware.RealIP)
	router.Use(middleware.RequestID)
	router.Use(middleware.Recoverer)

	// CORS setup.
	corsMiddleware := cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD"},
		AllowedHeaders: []string{
			"Accept", "Accept-Ranges", "Accept-Language", "Access-Control-Request-Private-Network",
			"Authorization", "Content-Language", "Content-Type", "Content-Length", "Origin", "Range",
		},
		ExposedHeaders:   []string{"Content-Range"},
		AllowCredentials: true,
		MaxAge:           600,
	})
	router.Use(corsMiddleware.Handler)
	router.Use(tSCorrectionMiddleware)
	router.Use(tSUploadTorrentMiddleware)

	currentSettings := c.settings.Load()
	if *currentSettings.EnableDlna {
		router.Mount(c.dlnaPath, c.dlna)
	}

	// Posters routes.
	postersHandler := http.StripPrefix(c.postersPath, c.images)
	router.Mount(c.postersPath, postersHandler)

	// Metrics routes.
	router.Method(http.MethodGet, "/metrics", c.metrics.Handler())

	// Swagger routes.
	router.Get("/swagger/openapi.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(swagger)
	})

	router.Get("/swagger", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/swagger/", http.StatusPermanentRedirect)
	})

	router.Method(http.MethodGet, "/swagger/*", v5emb.New(
		swagger.Info.Title,
		"/swagger/openapi.json",
		"/swagger/",
	))

	// API routes with middleware.
	router.Route("/", func(r chi.Router) {
		r.Use(
			nethttpmiddleware.OapiRequestValidatorWithOptions(
				swagger, &nethttpmiddleware.Options{
					Options: openapi3filter.Options{
						AuthenticationFunc: c.NewAuthenticator(),
					},
					ErrorHandlerWithOpts: c.ErrorHandler,
				},
			),
		)
		api.HandlerFromMux(c, r)
	})

	router.Get("/", web.ServeStatic())
	router.Get("/demo", web.ServeStatic(func(r *http.Request) {
		r.URL.Path = "static/demo.html"
	}))
	router.Get("/{file:.*\\.(html|md|png|svg|txt)}", web.ServeStatic())
	router.Get("/_next/*", web.ServeStatic())

	return router
}

// SetupRouter initializes the router. It is idempotent and thread-safe.
func (c *Controller) SetupRouter() *chi.Mux {
	c.mu.RLock()
	if c.router != nil {
		c.mu.RUnlock()
		return c.router
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// After acquiring the write lock, we must check again in case another
	// goroutine initialized the router while we were waiting for the lock.
	if c.router != nil {
		return c.router
	}

	c.router = c.buildRouter()
	return c.router
}

func (c *Controller) NewAuthenticator() openapi3filter.AuthenticationFunc {
	return func(ctx context.Context, input *openapi3filter.AuthenticationInput) error {
		currentSettings := c.settings.Load()

		if !utils.Val(currentSettings.Auth.Enabled) {
			return nil
		}

		authType := utils.Val(currentSettings.Auth.Type)
		username := utils.Val(currentSettings.Auth.Username)
		password := utils.Val(currentSettings.Auth.Password)

		if username == "" || password == "" {
			return errors.New("authentication not configured correctly")
		}

		var jwtSecret string
		if authType == api.Bearer {
			var err error
			jwtSecret, err = c.db.GetJWTSecret()
			if err != nil {
				return errors.New("authentication not configured correctly")
			}
			if jwtSecret == "" {
				return errors.New("authentication not configured correctly")
			}
		}

		switch input.SecuritySchemeName {
		case "basicAuth":
			if authType != api.Basic {
				return errors.New("basic authentication is not enabled")
			}
			authHeader := input.RequestValidationInput.Request.Header.Get("Authorization")
			if authHeader == "" {
				return &api.AuthError{Message: "authorization header is missing", Type: "Basic"}
			}
			user, pass, ok := input.RequestValidationInput.Request.BasicAuth()
			if !ok {
				return &api.AuthError{Message: "invalid basic auth format", Type: "Basic"}
			}
			if user != username || pass != password {
				return &api.AuthError{Message: "invalid credentials", Type: "Basic"}
			}
			return nil
		case "bearerAuth":
			if authType != api.Bearer {
				return errors.New("bearer authentication is not enabled")
			}
			authHeader := input.RequestValidationInput.Request.Header.Get("Authorization")
			if authHeader == "" {
				return &api.AuthError{Message: "authorization header is missing", Type: "Bearer"}
			}

			tokenString := strings.TrimPrefix(authHeader, "Bearer ")
			if _, err := auth.ValidateToken(tokenString, []byte(jwtSecret)); err != nil {
				return &api.AuthError{Message: fmt.Sprintf("invalid token: %v", err), Type: "Bearer"}
			}
			return nil
		case "cookieAuth":
			if authType != api.Bearer {
				return nil
			}
			cookie, err := input.RequestValidationInput.Request.Cookie("session")
			if err != nil {
				return &api.AuthError{Message: "missing session cookie", Type: "cookie"}
			}
			if _, err := auth.ValidateToken(cookie.Value, []byte(jwtSecret)); err != nil {
				return &api.AuthError{Message: fmt.Sprintf("invalid token: %v", err), Type: "cookie"}
			}
			return nil
		}

		return errors.New("authentication failed")
	}
}

func (c *Controller) SlogMiddleware() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			logger := c.logger.Load()

			logAttrs := []any{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
			}
			if r.URL.RawQuery != "" {
				logAttrs = append(logAttrs, slog.String("query", r.URL.RawQuery))
			}
			logAttrs = append(logAttrs, slog.String("user-agent", r.UserAgent()))

			logger = logger.With(logAttrs...)

			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			t1 := time.Now()

			defer func() {
				logger.Debug("request completed",
					"status", ww.Status(),
					"duration", time.Since(t1),
				)
			}()

			next.ServeHTTP(ww, r)
		})
	}
}

func (c *Controller) MetricsMiddleware() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()

			defer func() {
				// Use chi.RouteContext to get the route pattern.
				routeCtx := chi.RouteContext(r.Context())
				routePath := routeCtx.RoutePattern()
				// Sometimes path is empty, for example for static files.
				// In that case, we can use r.URL.Path.
				if routePath == "" {
					routePath = r.URL.Path
				}

				duration := time.Since(start)
				statusCode := strconv.Itoa(ww.Status())
				method := r.Method

				c.metrics.HTTPRequestsTotal.WithLabelValues(statusCode, method, routePath).Inc()
				c.metrics.HTTPRequestDuration.WithLabelValues(statusCode, method, routePath).Observe(duration.Seconds())

				// r.ContentLength can be -1 if the size is unknown.
				if r.ContentLength > 0 {
					c.metrics.HTTPRequestSizeBytes.WithLabelValues(statusCode, method, routePath).Observe(float64(r.ContentLength))
				}

				c.metrics.HTTPResponseSizeBytes.WithLabelValues(statusCode, method, routePath).Observe(float64(ww.BytesWritten()))
			}()

			next.ServeHTTP(ww, r)
		})
	}
}

func (c *Controller) Start() {
	c.logger.Load().Info("starting TorrPlay...")
	c.logger.Load().Info("build info", "commit", buildinfo.Commit, "version", buildinfo.Version, "build date", buildinfo.BuildDate)
	c.reconcileProfiler()

	addr := net.JoinHostPort(c.httpAddr, strconv.Itoa(c.resolveHTTPPort()))
	c.httpServer = httpserver.NewServer(c.SetupRouter(), addr, c.logger.Load())

	go func() {
		if err := c.httpServer.Run(); err != nil {
			c.logger.Load().Error("HTTP server stopped with error", "error", err)
		}
	}()
}

func (c *Controller) Shutdown() {
	c.logger.Load().Info("shutting down TorrPlay...")

	if c.httpServer != nil {
		_ = c.httpServer.Close()
	}
	c.stopProfiler()

	if activeDownloader := c.downloader.Load(); activeDownloader != nil {
		activeDownloader.Stop()
	}

	close(c.torrentTracker.cleanupDone)
	close(c.posterCleanupDone)

	_ = c.dlna.Stop()
	if pool := c.streamPool.Load(); pool != nil {
		pool.Close()
	}
	if c.storageClient.Load() != nil {
		_ = c.storageClient.Load().Close()
	}
	if c.pieceCompletion != nil {
		_ = c.pieceCompletion.Close()
	}
	if c.client != nil {
		_ = c.client.Close()
	}
	c.stopPosterWorkers()
	if c.images != nil {
		_ = c.images.Close()
	}
	if c.logFile != nil {
		_ = c.logFile.Close()
	}

	c.logger.Load().Info("TorrPlay stopped")
}

func (c *Controller) startPosterWorker(worker func()) bool {
	c.posterWorkersMu.Lock()
	defer c.posterWorkersMu.Unlock()

	if c.posterWorkersStopped {
		return false
	}

	c.posterWorkers.Go(worker)
	return true
}

func (c *Controller) stopPosterWorkers() {
	c.posterWorkersMu.Lock()
	c.posterWorkersStopped = true
	c.posterWorkersMu.Unlock()

	c.posterWorkers.Wait()
}

func profilerHandler() http.Handler {
	router := chi.NewRouter()
	router.Mount("/debug", middleware.Profiler())
	return router
}

func (c *Controller) reconcileProfiler() {
	c.profilerMu.Lock()
	enabled := utils.Val(c.settings.Load().LogLevel) == slog.LevelDebug

	if !enabled {
		server := c.detachProfilerLocked()
		c.profilerMu.Unlock()
		c.shutdownProfiler(server)
		return
	}
	defer c.profilerMu.Unlock()

	if c.profilerServer != nil {
		return
	}

	listener, err := net.Listen("tcp4", c.profilerAddr)
	if err != nil {
		c.logger.Load().Error("failed to start profiler server", "address", c.profilerAddr, "error", err)
		return
	}

	server := &http.Server{
		Handler:           profilerHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	c.profilerListener = listener
	c.profilerServer = server
	c.logger.Load().Info("starting profiler server", "address", listener.Addr().String())

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			c.logger.Load().Error("profiler server stopped with error", "error", err)
		}
	}()
}

func (c *Controller) stopProfiler() {
	c.profilerMu.Lock()
	server := c.detachProfilerLocked()
	c.profilerMu.Unlock()
	c.shutdownProfiler(server)
}

func (c *Controller) detachProfilerLocked() *http.Server {
	server := c.profilerServer
	c.profilerServer = nil
	c.profilerListener = nil
	return server
}

func (c *Controller) shutdownProfiler(server *http.Server) {
	if server == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), profilerShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		c.logger.Load().Error("failed to shut down profiler server", "error", err)
		_ = server.Close()
	}
}

func (c *Controller) startTorrentCleanup() {
	c.torrentTracker.cleanupTicker = time.NewTicker(5 * time.Minute)
	defer c.torrentTracker.cleanupTicker.Stop()

	for {
		select {
		case <-c.torrentTracker.cleanupTicker.C:
			c.cleanupExpiredTorrents()
		case <-c.torrentTracker.cleanupDone:
			return
		}
	}
}

func (c *Controller) startPosterCleanup() {
	c.posterCleanupTicker = time.NewTicker(posterCleanupInterval)
	defer c.posterCleanupTicker.Stop()

	for {
		select {
		case <-c.posterCleanupTicker.C:
			c.cleanupUnusedPosters()
		case <-c.posterCleanupDone:
			return
		}
	}
}

func (c *Controller) cleanupUnusedPosters() {
	c.posterOpMu.Lock()
	defer c.posterOpMu.Unlock()

	ids, err := c.images.ListIDs()
	if err != nil {
		c.logger.Load().Error("failed to list image ids", "err", err)
		return
	}

	for _, id := range ids {
		isUsed, err := c.db.IsPosterUsed(id)
		if err != nil {
			c.logger.Load().Error("failed to check if poster is used", "err", err)
			continue
		}

		if !isUsed {
			if err := c.images.Delete(id); err != nil {
				c.logger.Load().Error("failed to delete unused poster", "err", err)
			} else {
				c.logger.Load().Debug("deleted unused poster", "poster id", id)
			}
		}
	}
}

func (c *Controller) cleanupExpiredTorrents() {
	c.torrentTracker.mu.Lock()
	defer c.torrentTracker.mu.Unlock()

	c.logger.Load().Debug("cleanup expired torrents", "total", len(c.torrentTracker.torrents))
	now := time.Now()

	for ih, info := range c.torrentTracker.torrents {
		sub := now.Sub(info.lastUsedAt)
		if sub > c.torrentTracker.ttl {
			// Check if the torrent is actively being streamed or downloaded in the background.
			isStreaming, isDownloading := c.torrentActivity(ih)

			if isStreaming || isDownloading {
				info.lastUsedAt = now
				c.torrentTracker.torrents[ih] = info
				c.logger.Load().Debug("skipping expiration for active torrent", "hash", ih)
				continue
			}

			c.logger.Load().Debug("mark torrent as expired", "hash", ih, "age", sub)
			c.cancelPreload(ih)
			delete(c.torrentTracker.torrents, ih)
			if to, ok := c.clientTorrent(ih); ok {
				go func(t *torrent.Torrent, hash metainfo.Hash, age time.Duration) {
					t.Drop()
					<-t.Closed()
					c.logger.Load().Debug("dropped torrent", "hash", hash, "age", age)
				}(to, ih, sub)
			}
		}
	}
}

// hasTorrentReaders returns true if the torrent has any stream pool readers
// (active or lingering) or is being downloaded by the background downloader.
// Lingering readers are included because they are still downloading for the
// player's next request — see stream.Pool.HasReaders for details.
func (c *Controller) hasTorrentReaders(ih metainfo.Hash) bool {
	isStreaming, isDownloading := c.torrentActivity(ih)
	return isStreaming || isDownloading
}

// torrentActivity reports whether the torrent has stream pool readers
// and whether the background downloader is working on it.
// It takes no controller lock, so callers may hold c.mu.
func (c *Controller) torrentActivity(ih metainfo.Hash) (isStreaming, isDownloading bool) {
	if pool := c.streamPool.Load(); pool != nil {
		isStreaming = pool.HasReaders(ih) || c.preloadActive(ih)
	}
	if activeDownloader := c.downloader.Load(); activeDownloader != nil {
		isDownloading = activeDownloader.IsActive(ih)
	}
	return isStreaming, isDownloading
}

// resolveHTTPPort determines the definitive port for the HTTP server by selecting between two possible sources.
// It gives precedence to the port number provided at application startup
// over the port number stored in the application's persistent settings.
func (c *Controller) resolveHTTPPort() int {
	if c.port < 1 || c.port > 65535 {
		return *c.settings.Load().HTTPServerPort
	}

	return c.port
}

// clientTorrent looks up a torrent in the active client. It reports false when
// no client is configured.
func (c *Controller) clientTorrent(ih metainfo.Hash) (*torrent.Torrent, bool) {
	client := c.currentClient()
	if client == nil {
		return nil, false
	}
	return client.Torrent(ih)
}

// currentClient returns the active torrent client, or nil before the first
// configuration. Callers that already hold c.mu must read c.client directly.
func (c *Controller) currentClient() *torrent.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.client
}

func (c *Controller) configureTorrentClient() error {
	c.torrentConfigMu.Lock()
	defer c.torrentConfigMu.Unlock()

	c.mu.RLock()
	oldClient := c.client
	oldStorageClient := c.storageClient.Load()
	oldPool := c.streamPool.Load()
	currentSettings := c.settings.Load()
	logger := c.logger.Load()
	isReconfiguring := c.downloader.Load() != nil
	c.mu.RUnlock()

	var newTrackers [][]string
	if isReconfiguring {
		if currentSettings.TorrentTrackers != nil && len(*currentSettings.TorrentTrackers) > 0 {
			for _, tracker := range *currentSettings.TorrentTrackers {
				newTrackers = append(newTrackers, strings.Split(tracker, ","))
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		fetchedTrackers, err := c.runtimeConfig.fetchTrackers(ctx, c.httpClient)
		if err != nil {
			logger.Debug(fmt.Sprintf("failed to get trackers, %v", err.Error()))
		}
		newTrackers = append(newTrackers, fetchedTrackers...)
		cancel()
	}

	if oldClient != nil {
		c.torrentClientUnavailable.Store(true)
		_ = oldClient.Close()
		<-oldClient.Closed()
		if c.runtimeConfig.clientCloseDelay > 0 {
			<-time.After(c.runtimeConfig.clientCloseDelay)
		}
	}
	if oldStorageClient != nil {
		_ = oldStorageClient.Close()
		<-oldStorageClient.Closed()
	}

	// Close old stream pool.
	if oldPool != nil {
		oldPool.Close()
	}

	clientConfig := torrent.NewDefaultClientConfig()
	if currentSettings.TorrentClient != nil {
		clientConfig.NoDHT = utils.Val(currentSettings.TorrentClient.DisableDHT)
		clientConfig.DisableIPv6 = utils.Val(currentSettings.TorrentClient.DisableIPv6)
		clientConfig.DisablePEX = utils.Val(currentSettings.TorrentClient.DisablePEX)
		clientConfig.DisableTCP = utils.Val(currentSettings.TorrentClient.DisableTCP)
		clientConfig.DisableUTP = utils.Val(currentSettings.TorrentClient.DisableUTP)
		if limit := utils.Val(currentSettings.TorrentClient.DownloadRateLimit); limit > 0 {
			clientConfig.DownloadRateLimiter = rate.NewLimiter(rate.Limit(limit), 0)
		}
		clientConfig.EstablishedConnsPerTorrent = utils.Val(currentSettings.TorrentClient.EstablishedConnsPerTorrent)
		clientConfig.HeaderObfuscationPolicy.RequirePreferred = utils.Val(currentSettings.TorrentClient.PreferHeaderObfuscation)
		clientConfig.Seed = utils.Val(currentSettings.TorrentClient.Seed)
		clientConfig.TorrentPeersHighWater = utils.Val(currentSettings.TorrentClient.TorrentPeersHighWater)
		if limit := utils.Val(currentSettings.TorrentClient.UploadRateLimit); limit > 0 {
			clientConfig.UploadRateLimiter = rate.NewLimiter(rate.Limit(limit), 0)
		}
	}
	clientConfig.ExtendedHandshakeClientVersion = "qBittorrent/5.1.4"
	clientConfig.ListenPort = 0
	// The torrent client logs only errors, whatever the application log level.
	clientConfig.Slogger = c.configureLogger(slog.LevelError, currentSettings)
	if c.runtimeConfig.configureClient != nil {
		c.runtimeConfig.configureClient(clientConfig)
	}

	storageClient := memstorage.New(*currentSettings.MaxMemory, logger)
	clientConfig.DefaultStorage = storageClient

	client, err := torrent.NewClient(clientConfig)
	if err != nil {
		return fmt.Errorf("failed to initiate torrent client: %w", err)
	}

	// Create new stream pool.
	// Registry: storageClient protects actively-read pieces from eviction
	// by registering readahead windows with the storage layer.
	pool := stream.New(stream.Config{
		FileReadaheadBytes:     fileStorageReadahead,
		LingerTimeout:          30 * time.Second,
		Logger:                 logger,
		PriorityWindowFraction: 0.15,
		Registry:               storageClient,
	})
	pool.SetReadaheadBudget(readaheadBudget(*currentSettings.MaxMemory))
	// Evicted pieces must stop counting as downloaded, or a torrent read in
	// full looks complete to the client, which then drops its peers.
	storageClient.SetEvictionHandler(memstorage.ClientEvictionHandler(client))

	c.mu.Lock()
	c.client = client
	c.storageClient.Store(storageClient)
	c.streamPool.Store(pool)
	if isReconfiguring {
		c.downloader.Load().Stop()
		c.trackers = newTrackers
		newDownloader := downloader.New(c.client, c.db, c.logger.Load(), c.metrics, c.pieceCompletion, utils.Val(currentSettings.FileStoragePath), c.trackers)
		c.downloader.Store(newDownloader)
		if utils.Val(currentSettings.EnableDownloader) {
			newDownloader.Start()
		}
	}
	c.torrentGeneration.Add(1)
	c.mu.Unlock()
	c.torrentClientUnavailable.Store(false)

	return nil
}

func (c *Controller) configureLogger(level slog.Level, st *api.Settings) *slog.Logger {
	var writer io.Writer = os.Stdout

	if _, isService := os.LookupEnv("TORRPLAY_RUNNING_AS_SERVICE"); isService {
		logFilePath := filepath.Join(c.dataDir, "torrplay.log")
		lj := &lumberjack.Logger{
			Filename:   logFilePath,
			MaxSize:    5,
			MaxBackups: 3,
			MaxAge:     28,
			Compress:   true,
		}
		writer = lj
		c.logFile = lj
	}

	var handler slog.Handler

	slogOpts := &slog.HandlerOptions{Level: level}

	if level == slog.LevelDebug {
		slogOpts = &slog.HandlerOptions{
			AddSource: true,
			Level:     slog.LevelDebug,
		}
	}

	if *st.LogFormat == api.JSON {
		handler = slog.NewJSONHandler(writer, slogOpts)
	} else {
		handler = slog.NewTextHandler(writer, slogOpts)
	}

	storeHandler := logging.NewStoreHandler(handler, logging.DefaultStore)

	return slog.New(storeHandler)
}
