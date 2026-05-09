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
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	"github.com/torrplay/torrplay/internal/utils"
	memstorage "github.com/torrplay/torrplay/pkg/storage"
	"github.com/torrplay/torrplay/web"
	"gopkg.in/natefinch/lumberjack.v2"
)

const (
	fileStorageReadahead = 50 * 1024 * 1024 // 50MB
	// a threshold to prevent the "viewed" timestamp for a file from being updated too frequently.
	fileViewedUpdateThreshold = 5 * time.Minute
	gotInfoTimeoutMsg         = "timeout waiting for torrent metadata"
	imageDownloadTimeout      = 1 * time.Minute
	multipartFormMaxMemory    = 1 << 20
	posterCleanupInterval     = 6 * time.Hour
	torrentTrackerTTL         = 3 * time.Hour
)

var gotInfoTimeout = 30 * time.Second

var _ api.ServerInterface = (*Controller)(nil)

// torrentTracker tracks loaded torrents and drops those that have been inactive for a specified time-to-live (TTL).
// This prevents the system from being overloaded with unused torrents and automatically frees up their associated memory.
type torrentTracker struct {
	cleanupDone   chan struct{}
	cleanupTicker *time.Ticker
	mu            sync.RWMutex
	torrents      map[metainfo.Hash]time.Time
	ttl           time.Duration
}

type Controller struct {
	client              *torrent.Client
	dataDir             string
	db                  database.DatabaseInterface
	dlna                *dlna.Service
	dlnaPath            string
	downloader          *downloader.Downloader
	httpAddr            string
	httpClient          *httpclient.Client
	httpServer          *httpserver.Server
	images              images.ServiceInterface
	logFile             io.Closer
	logger              *slog.Logger
	metrics             *metrics.Metrics
	mu                  sync.RWMutex
	port                int
	pieceCompletion     piececompletion.DeletablePieceCompletion
	posterCleanupDone   chan struct{}
	posterCleanupTicker *time.Ticker
	posterOpMu          sync.Mutex
	postersPath         string
	router              *chi.Mux
	settings            *api.Settings
	startedAt           time.Time
	storageClient       *memstorage.Client
	torrentTracker      torrentTracker
	trackers            [][]string

	api.Unimplemented
}

func NewController(dataDir string, ipAddr string, port int, dbClient database.DatabaseInterface, images images.ServiceInterface, metrics *metrics.Metrics) (*Controller, error) {
	settings, err := dbClient.GetSettings()
	if err != nil {
		return nil, err
	}

	logging.DefaultStore.Resize(utils.Val(settings.LogStoreSize))

	c := &Controller{
		dataDir:           dataDir,
		db:                dbClient,
		dlnaPath:          "/upnp/",
		images:            images,
		httpAddr:          ipAddr,
		httpClient:        httpclient.New(),
		metrics:           metrics,
		port:              port,
		posterCleanupDone: make(chan struct{}),
		postersPath:       "/posters/",
		settings:          settings,
		startedAt:         time.Now(),
		torrentTracker: torrentTracker{
			cleanupDone: make(chan struct{}),
			torrents:    make(map[metainfo.Hash]time.Time),
			ttl:         torrentTrackerTTL,
		},
	}

	c.logger = c.configureLogger(*settings.LogLevel)

	var pc piececompletion.DeletablePieceCompletion
	if fsp := utils.Val(settings.FileStoragePath); fsp != "" {
		if _, err := os.Stat(fsp); os.IsNotExist(err) {
			if err := os.MkdirAll(fsp, 0755); err != nil {
				return nil, fmt.Errorf("failed to create file storage directory: %w", err)
			}
		}
		pc, err = piececompletion.New(fsp, c.logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create piece completion database: %w", err)
		}
	}
	c.pieceCompletion = pc

	c.dlna = dlna.NewService(dbClient, images, c.dlnaPath, c.postersPath, c.logger)

	// Check for auth override environment variable. This allows a user to regain
	// access to their settings if they have forgotten their credentials.
	if authOverride, exists := os.LookupEnv("TORRPLAY_DISABLE_AUTH"); exists {
		if enabled, err := strconv.ParseBool(authOverride); err == nil && enabled {
			if c.settings.Auth == nil {
				c.settings.Auth = &api.Auth{}
			}
			c.settings.Auth.Enabled = utils.Ptr(false)
			c.logger.Warn("Authentication has been disabled via the TORRPLAY_DISABLE_AUTH environment variable.")
		}
	}

	c.logger.Info(fmt.Sprintf("initializing TorrPlay with data directory: %s", c.dataDir))

	trackers, err := c.fetchTrackers()
	if err != nil {
		c.logger.Debug(fmt.Sprintf("failed to get trackers, %v", err.Error()))
	}
	c.trackers = trackers

	err = c.configureTorrentClient(slog.LevelError)
	if err != nil {
		return nil, err
	}

	c.downloader = downloader.New(c.client, c.db, c.logger, c.metrics, c.pieceCompletion, utils.Val(c.settings.FileStoragePath), c.trackers)

	if *settings.EnableDlna {
		err = c.dlna.Start(*c.settings.FriendlyName, c.httpAddr, c.resolveHTTPPort())
		if err != nil {
			return nil, err
		}
	}

	if *settings.EnableDownloader {
		c.downloader.Start()
	}

	go c.startTorrentCleanup()
	go c.startPosterCleanup()

	return c, nil
}

func (c *Controller) Logger() *slog.Logger {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.logger
}

func (c *Controller) Settings() *api.Settings {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.settings
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

	if *c.settings.EnableDlna {
		router.Mount(c.dlnaPath, c.dlna)
	}

	if *c.settings.LogLevel == slog.LevelDebug {
		router.Mount("/debug", middleware.Profiler())
	}

	// Posters routes.
	postersHandler := http.StripPrefix(c.postersPath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		ext := path.Ext(p)
		r.URL.Path = strings.TrimSuffix(p, ext)
		c.images.ServeHTTP(w, r)
	}))
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
		c.mu.RLock()
		settings := c.settings
		c.mu.RUnlock()

		if !utils.Val(settings.Auth.Enabled) {
			return nil
		}

		authType := utils.Val(settings.Auth.Type)
		username := utils.Val(settings.Auth.Username)
		password := utils.Val(settings.Auth.Password)

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
			c.mu.RLock()
			logger := c.logger
			c.mu.RUnlock()

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
				path := routeCtx.RoutePattern()
				// Sometimes path is empty, for example for static files.
				// In that case, we can use r.URL.Path.
				if path == "" {
					path = r.URL.Path
				}

				duration := time.Since(start)
				statusCode := strconv.Itoa(ww.Status())
				method := r.Method

				c.metrics.HTTPRequestsTotal.WithLabelValues(statusCode, method, path).Inc()
				c.metrics.HTTPRequestDuration.WithLabelValues(statusCode, method, path).Observe(duration.Seconds())

				// r.ContentLength can be -1 if the size is unknown.
				if r.ContentLength > 0 {
					c.metrics.HTTPRequestSizeBytes.WithLabelValues(statusCode, method, path).Observe(float64(r.ContentLength))
				}

				c.metrics.HTTPResponseSizeBytes.WithLabelValues(statusCode, method, path).Observe(float64(ww.BytesWritten()))
			}()

			next.ServeHTTP(ww, r)
		})
	}
}

func (c *Controller) Start() {
	c.logger.Info("starting TorrPlay...")
	c.logger.Info("build info", "commit", buildinfo.Commit, "version", buildinfo.Version, "build date", buildinfo.BuildDate)

	addr := net.JoinHostPort(c.httpAddr, strconv.Itoa(c.resolveHTTPPort()))
	c.httpServer = httpserver.NewServer(c.SetupRouter(), addr, c.logger)

	go func() {
		if err := c.httpServer.Run(); err != nil {
			c.logger.Error("HTTP server stopped with error", "error", err)
		}
	}()
}

func (c *Controller) Shutdown() {
	c.logger.Info("shutting down TorrPlay...")

	if c.httpServer != nil {
		_ = c.httpServer.Shutdown()
	}

	if c.downloader != nil {
		c.downloader.Stop()
	}

	close(c.torrentTracker.cleanupDone)
	close(c.posterCleanupDone)

	_ = c.dlna.Stop()
	_ = c.storageClient.Close()
	if c.pieceCompletion != nil {
		_ = c.pieceCompletion.Close()
	}
	_ = c.client.Close()
	if c.logFile != nil {
		_ = c.logFile.Close()
	}

	c.logger.Info("TorrPlay stopped")
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
		c.logger.Error("failed to list image ids", "err", err)
		return
	}

	for _, id := range ids {
		isUsed, err := c.db.IsPosterUsed(id)
		if err != nil {
			c.logger.Error("failed to check if poster is used", "err", err)
			continue
		}

		if !isUsed {
			if err := c.images.Delete(&id); err != nil {
				c.logger.Error("failed to delete unused poster", "err", err)
			} else {
				c.logger.Debug("deleted unused poster", "poster id", id)
			}
		}
	}
}

func (c *Controller) cleanupExpiredTorrents() {
	c.torrentTracker.mu.Lock()
	defer c.torrentTracker.mu.Unlock()

	c.logger.Debug("cleanup expired torrents", "total", len(c.torrentTracker.torrents))
	now := time.Now()

	for ih, lastUsedAt := range c.torrentTracker.torrents {
		sub := now.Sub(lastUsedAt)
		if sub > c.torrentTracker.ttl {
			c.logger.Debug("mark torrent as expired", "hash", ih, "age", sub)
			delete(c.torrentTracker.torrents, ih)
			if to, ok := c.client.Torrent(ih); ok {
				if to.Stats().ActivePeers == 0 {
					go func() {
						to.Drop()
						<-to.Closed()
						c.logger.Debug("dropped torrent", "hash", ih, "age", sub)
					}()
				}
			}
		}
	}
}

// resolveHTTPPort determines the definitive port for the HTTP server by selecting between two possible sources.
// It gives precedence to the port number provided at application startup
// over the port number stored in the application's persistent settings.
func (c *Controller) resolveHTTPPort() int {
	if c.port < 1 || c.port > 65535 {
		return *c.settings.HTTPServerPort
	}

	return c.port
}

func (c *Controller) configureTorrentClient(clientLevel slog.Level) error {
	c.mu.RLock()
	oldClient := c.client
	oldStorageClient := c.storageClient
	settings := c.settings
	logger := c.logger
	c.mu.RUnlock()

	if oldClient != nil {
		_ = oldClient.Close()
		<-oldClient.Closed()
		<-time.After(500 * time.Millisecond)
	}
	if oldStorageClient != nil {
		_ = oldStorageClient.Close()
		<-oldStorageClient.Closed()
	}

	clientConfig := torrent.NewDefaultClientConfig()
	clientConfig.DisableIPv6 = *settings.DisableIpv6
	clientConfig.ExtendedHandshakeClientVersion = "qBittorrent/5.1.4"
	clientConfig.HeaderObfuscationPolicy.RequirePreferred = true
	clientConfig.ListenPort = 0
	clientConfig.Slogger = c.configureLogger(clientLevel)

	storageClient := memstorage.NewClient(*settings.MaxMemory, logger)
	clientConfig.DefaultStorage = storageClient

	client, err := torrent.NewClient(clientConfig)
	if err != nil {
		return fmt.Errorf("failed to initiate torrent client: %w", err)
	}

	c.mu.Lock()
	c.client = client
	c.storageClient = storageClient
	c.mu.Unlock()

	return nil
}

func (c *Controller) configureLogger(level slog.Level) *slog.Logger {
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

	if *c.settings.LogFormat == api.JSON {
		handler = slog.NewJSONHandler(writer, slogOpts)
	} else {
		handler = slog.NewTextHandler(writer, slogOpts)
	}

	storeHandler := logging.NewStoreHandler(handler, logging.DefaultStore)

	return slog.New(storeHandler)
}

func (c *Controller) fetchTrackers() ([][]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	url := "https://raw.githubusercontent.com/ngosang/trackerslist/master/trackers_best_ip.txt"

	resp, err := c.httpClient.Get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP request failed with status: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Convert to string and and split by lines.
	content := string(body)
	lines := strings.Split(content, "\n")

	// Filter out empty lines and create a slice.
	var trackers []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			trackers = append(trackers, line)
		}
	}

	return [][]string{trackers}, nil
}
