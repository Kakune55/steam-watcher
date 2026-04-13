package web

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"steam-watcher/internal/app"
	"steam-watcher/internal/config"
	"steam-watcher/internal/logx"
	"steam-watcher/internal/store"
)

//go:embed templates/*
var templateFS embed.FS

type Server struct {
	echo      *echo.Echo
	templates *template.Template
	cfg       config.Config
	store     *store.Store
	collector *app.Collector

	updateMu       sync.RWMutex
	updateVersion  uint64
	updateSignal   chan struct{}
	shutdownSignal chan struct{}
	shuttingDown   bool
	dashboardCache dashboardPayload
}

type dashboardPayload struct {
	Version  string                     `json:"version"`
	Statuses []store.LatestFriendStatus `json:"statuses"`
	Runs     []store.Run                `json:"runs"`
}

func NewServer(cfg config.Config, db *store.Store, collector *app.Collector) *Server {
	e := echo.New()
	e.Logger = slog.Default().With("component", "http")
	e.Use(middleware.Recover())
	e.Use(logx.GetEchoLogger(e.Logger))

	server := &Server{
		echo:           e,
		templates:      template.Must(template.ParseFS(templateFS, "templates/*.html")),
		cfg:            cfg,
		store:          db,
		collector:      collector,
		updateSignal:   make(chan struct{}),
		shutdownSignal: make(chan struct{}),
	}
	server.collector.SetOnChange(server.notifyDataChanged)

	e.GET("/", server.handleIndex)
	e.GET("/api/status/latest", server.handleLatestStatuses)
	e.GET("/api/friends/:friendSteamID/history", server.handleFriendHistory)
	e.GET("/api/runs", server.handleRuns)
	e.GET("/api/dashboard", server.handleDashboard)
	e.POST("/api/collect", server.handleCollect)

	return server
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.echo.ServeHTTP(w, r)
}

func (s *Server) Start(address string) error {
	return s.echo.Start(address)
}

func (s *Server) NotifyShutdown() {
	s.updateMu.Lock()
	s.shuttingDown = true
	select {
	case <-s.shutdownSignal:
	default:
		close(s.shutdownSignal)
	}
	select {
	case <-s.updateSignal:
	default:
		close(s.updateSignal)
	}
	s.updateMu.Unlock()
}

func (s *Server) handleIndex(c *echo.Context) error {
	statuses, err := s.store.LatestStatuses()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	runs, err := s.store.RecentRuns(10)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	return s.templates.ExecuteTemplate(c.Response(), "index.html", map[string]any{
		"Statuses": statuses,
		"Runs":     runs,
		"Config":   s.cfg,
	})
}

func (s *Server) handleLatestStatuses(c *echo.Context) error {
	payload, err := s.loadDashboardPayload(false)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"items": payload.Statuses})
}

func (s *Server) handleDashboard(c *echo.Context) error {
	const maxWait = 25 * time.Second

	since := c.QueryParam("since")
	version, changed, err := s.waitForDashboardVersion(c.Request().Context(), since, maxWait)
	if err != nil {
		return c.JSON(http.StatusRequestTimeout, map[string]string{"error": err.Error()})
	}
	if !changed {
		return c.JSON(http.StatusOK, map[string]any{
			"changed": false,
			"version": version,
		})
	}

	payload, err := s.loadDashboardPayload(false)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{
		"changed":  true,
		"version":  payload.Version,
		"statuses": payload.Statuses,
		"runs":     payload.Runs,
	})
}

func (s *Server) handleRuns(c *echo.Context) error {
	payload, err := s.loadDashboardPayload(false)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"items": payload.Runs})
}

func (s *Server) handleFriendHistory(c *echo.Context) error {
	friendSteamID := c.Param("friendSteamID")
	if friendSteamID == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing friendSteamID"})
	}

	view := c.QueryParam("view")
	if view == "" {
		view = "halfyear"
	}

	tzOffsetMinutes, err := parseTZOffsetMinutes(c.QueryParam("tz_offset_minutes"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	now := time.Now().UTC()
	localNow := now.Add(time.Duration(tzOffsetMinutes) * time.Minute)
	var start time.Time
	var end time.Time
	var bucketUnit string

	switch view {
	case "year":
		start, end = localRangeToUTC(localNow.AddDate(0, 0, -364), localNow.AddDate(0, 0, 1), tzOffsetMinutes)
		bucketUnit = "day"
	case "halfyear":
		start, end = localRangeToUTC(localNow.AddDate(0, 0, -179), localNow.AddDate(0, 0, 1), tzOffsetMinutes)
		bucketUnit = "day"
	case "week":
		start, end = localRangeToUTC(localNow.AddDate(0, 0, -6), localNow.AddDate(0, 0, 1), tzOffsetMinutes)
		bucketUnit = "hour"
	case "day":
		dateValue := c.QueryParam("date")
		if dateValue == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "date is required for day view"})
		}

		parsed, err := time.Parse("2006-01-02", dateValue)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "date must be YYYY-MM-DD"})
		}

		start, end = localRangeToUTC(parsed, parsed.Add(24*time.Hour), tzOffsetMinutes)
		bucketUnit = "raw"
	default:
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "view must be one of: year, halfyear, week, day"})
	}

	meta, err := s.store.FriendHistoryMeta(friendSteamID)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "friend not found"})
	}

	items, err := s.store.FriendHistory(friendSteamID, start, end, bucketUnit, tzOffsetMinutes)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{
		"meta":        meta,
		"view":        view,
		"bucket_unit": bucketUnit,
		"start":       start,
		"end":         end,
		"items":       items,
	})
}

func parseTZOffsetMinutes(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("tz_offset_minutes must be an integer")
	}
	if offset < -840 || offset > 840 {
		return 0, fmt.Errorf("tz_offset_minutes out of range")
	}
	return offset, nil
}

func localRangeToUTC(localStart, localEnd time.Time, offsetMinutes int) (time.Time, time.Time) {
	start := time.Date(localStart.Year(), localStart.Month(), localStart.Day(), localStart.Hour(), localStart.Minute(), localStart.Second(), localStart.Nanosecond(), time.UTC)
	end := time.Date(localEnd.Year(), localEnd.Month(), localEnd.Day(), localEnd.Hour(), localEnd.Minute(), localEnd.Second(), localEnd.Nanosecond(), time.UTC)
	offset := time.Duration(offsetMinutes) * time.Minute
	return start.Add(-offset), end.Add(-offset)
}

func (s *Server) handleCollect(c *echo.Context) error {
	result, err := s.collector.CollectOnce(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, result)
}

func (s *Server) notifyDataChanged() {
	s.updateMu.Lock()
	if s.shuttingDown {
		s.updateMu.Unlock()
		return
	}
	s.updateVersion += 1
	s.dashboardCache = dashboardPayload{}
	close(s.updateSignal)
	s.updateSignal = make(chan struct{})
	s.updateMu.Unlock()
}

func (s *Server) currentDashboardVersion() string {
	s.updateMu.RLock()
	defer s.updateMu.RUnlock()
	return strconv.FormatUint(s.updateVersion, 10)
}

func (s *Server) waitForDashboardVersion(ctx context.Context, since string, timeout time.Duration) (string, bool, error) {
	s.updateMu.RLock()
	currentVersion := strconv.FormatUint(s.updateVersion, 10)
	signal := s.updateSignal
	s.updateMu.RUnlock()

	if since == "" || since != currentVersion {
		return currentVersion, true, nil
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return currentVersion, false, ctx.Err()
	case <-s.shutdownSignal:
		return currentVersion, false, context.Canceled
	case <-timer.C:
		return currentVersion, false, nil
	case <-signal:
		return s.currentDashboardVersion(), true, nil
	}
}

func (s *Server) loadDashboardPayload(forceReload bool) (dashboardPayload, error) {
	for {
		s.updateMu.RLock()
		cached := s.dashboardCache
		currentVersion := strconv.FormatUint(s.updateVersion, 10)
		s.updateMu.RUnlock()

		if !forceReload && cached.Version == currentVersion {
			return cached, nil
		}

		statuses, err := s.store.LatestStatuses()
		if err != nil {
			return dashboardPayload{}, err
		}

		runs, err := s.store.RecentRuns(20)
		if err != nil {
			return dashboardPayload{}, err
		}

		payload := dashboardPayload{
			Version:  currentVersion,
			Statuses: statuses,
			Runs:     runs,
		}

		s.updateMu.Lock()
		liveVersion := strconv.FormatUint(s.updateVersion, 10)
		if payload.Version == liveVersion {
			s.dashboardCache = payload
			s.updateMu.Unlock()
			return payload, nil
		}
		if s.dashboardCache.Version == liveVersion {
			payload = s.dashboardCache
			s.updateMu.Unlock()
			return payload, nil
		}
		s.updateMu.Unlock()
	}
}
