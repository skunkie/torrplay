// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package dlna

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/ethulhu/helix/upnp"
	"github.com/sirupsen/logrus"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/images"
	"github.com/torrplay/torrplay/internal/logging"
	"github.com/torrplay/torrplay/internal/utils"
)

const notifyInterval = 30 * time.Second

type Service struct {
	basePath         string
	cancel           context.CancelFunc
	contentDirectory *ContentDirectory
	db               database.DatabaseInterface
	device           *upnp.Device
	handler          http.Handler
	images           images.ServiceInterface
	logger           *slog.Logger
	mu               sync.RWMutex
	postersPath      string
}

func NewService(db database.DatabaseInterface, images images.ServiceInterface, basePath, postersPath string, logger *slog.Logger) *Service {
	// Configure the global logrus logger used by helix to use our slog hook.
	logrus.SetOutput(io.Discard)
	logrus.AddHook(logging.NewSlogHook(logger))

	return &Service{
		basePath:    basePath,
		db:          db,
		images:      images,
		logger:      logger,
		postersPath: postersPath,
	}
}

func (s *Service) IncrementSystemUpdateID() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.contentDirectory == nil {
		return
	}

	s.contentDirectory.IncrementSystemUpdateID()
}

func (s *Service) Reconfigure(friendlyName string, httpAddr string, port int) error {
	if err := s.Stop(); err != nil {
		return err
	}

	return s.Start(friendlyName, httpAddr, port)
}

func (s *Service) SendUpdateNotification() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.contentDirectory == nil || s.device == nil {
		return
	}

	if err := upnp.SendUpdateNotification(context.TODO(), s.device, s.contentDirectory.baseURL.String(), nil); err != nil {
		s.logger.Warn("could not send update notification", "err", err)
	}
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.handler == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.Header().Set("Cache-Control", "max-age=600")

	// serve icons.
	if strings.HasPrefix(r.URL.Path, path.Join(s.basePath, "/icons")) {
		http.StripPrefix(s.basePath, http.FileServerFS(iconsFS)).ServeHTTP(w, r)
		return
	}

	http.StripPrefix(strings.TrimRight(s.basePath, "/"), s.handler).ServeHTTP(w, r)
}

func (s *Service) SetLogger(logger *slog.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = logger

	// Atomically replace all hooks with the new one to avoid duplication.
	newHook := logging.NewSlogHook(logger)
	hooks := make(logrus.LevelHooks)
	for _, level := range logrus.AllLevels {
		hooks[level] = []logrus.Hook{newHook}
	}
	logrus.StandardLogger().ReplaceHooks(hooks)
}

func (s *Service) Start(friendlyName string, httpAddr string, port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cancel != nil {
		return fmt.Errorf("DLNA service is already running")
	}

	s.logger.Info("starting DLNA service")

	var ipAddr string
	// Use the specified IP address for DLNA, or discover a private IP address if the HTTP server is bound to 0.0.0.0.
	if net.ParseIP(httpAddr).IsUnspecified() {
		s.logger.Info(fmt.Sprintf("HTTP server is listening on all interfaces (%s), attempting to find a private IP address for DLNA", httpAddr))
		ip, err := utils.GetOutboundIP()
		if err != nil {
			s.logger.Warn("could not find outbound IP address, DLNA service will be disabled", "err", err)
			return nil // Return nil so the app can start without DLNA.
		} else {
			ipAddr = ip
			s.logger.Info("found outbound IP address for DLNA", "ip", ipAddr)
		}
	} else {
		ipAddr = httpAddr
	}

	isPrivate, err := utils.IsPrivateIPAddr(ipAddr)
	if err != nil {
		return fmt.Errorf("failed to check IP address: %w", err)
	}
	if !isPrivate {
		return fmt.Errorf("DLNA service can only be used on private networks, IP address: %s", ipAddr)
	}

	host := net.JoinHostPort(ipAddr, fmt.Sprintf("%d", port))

	baseURL, err := url.Parse(fmt.Sprintf("http://%s", host))
	baseURL.Path = s.basePath
	if err != nil {
		return fmt.Errorf("failed to parse base URL: %w", err)
	}

	icons, err := deviceIcons(iconsFS, "icons/device", baseURL)
	if err != nil {
		return fmt.Errorf("failed to load icons, %w", err)
	}

	udn, err := s.db.GetDLNAUDN()
	if err != nil {
		return fmt.Errorf("failed to get UDN, %w", err)
	}

	cd := NewContentDirectory(s.db, s.images, baseURL, s.postersPath)

	device := NewDevice(friendlyName, udn, icons, cd)

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	go func() {
		if err := upnp.BroadcastDevice(ctx, device, baseURL.String(), nil, notifyInterval); err != nil {
			s.logger.Error("failed to broadcast DLNA device", "err", err)
		}
	}()

	s.device = device
	s.handler = device.HTTPHandler(s.basePath)
	s.contentDirectory = cd
	s.device.SetBootID(uint(time.Now().Unix()))

	s.logger.Info("DLNA service is running")

	return nil
}

func (s *Service) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logger.Info("stopping DLNA service")

	if s.cancel != nil {
		if s.device != nil && s.contentDirectory != nil {
			ctx := context.TODO()
			upnp.NotifyByeBye(ctx, s.device, s.contentDirectory.baseURL.String(), nil)
		}

		s.cancel()
		s.cancel = nil
		s.device = nil
		s.handler = nil
		s.contentDirectory = nil
	}

	s.logger.Info("DLNA service stopped")

	return nil
}
