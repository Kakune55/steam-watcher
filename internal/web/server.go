package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"steam-watcher/internal/app"
	"steam-watcher/internal/config"
	"steam-watcher/internal/logx"
	"steam-watcher/internal/store"
)

//go:embed templates/* static/*
var assetFS embed.FS

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
	startedAt      time.Time
}

type dashboardPayload struct {
	Version  string                     `json:"version"`
	Statuses []store.LatestFriendStatus `json:"statuses"`
	Runs     []store.Run                `json:"runs"`
}

type settingsPayload struct {
	Config               config.EditableConfig `json:"config"`
	ConfigPath           string                `json:"config_path"`
	EnvironmentOverrides map[string]string     `json:"environment_overrides"`
	RequiresRestart      []string              `json:"requires_restart"`
}

type runtimePayload struct {
	GoVersion     string `json:"go_version"`
	Goroutines    int    `json:"goroutines"`
	GOMAXPROCS    int    `json:"gomaxprocs"`
	CPUCount      int    `json:"cpu_count"`
	MemoryAlloc   uint64 `json:"memory_alloc"`
	MemorySys     uint64 `json:"memory_sys"`
	HeapAlloc     uint64 `json:"heap_alloc"`
	HeapObjects   uint64 `json:"heap_objects"`
	NumGC         uint32 `json:"num_gc"`
	LastGCTime    string `json:"last_gc_time"`
	UptimeSeconds int64  `json:"uptime_seconds"`
}

type systemStatusPayload struct {
	DatabasePath      string         `json:"database_path"`
	DatabaseSizeBytes int64          `json:"database_size_bytes"`
	ConfigPath        string         `json:"config_path"`
	Collector         app.Status     `json:"collector"`
	Runtime           runtimePayload `json:"runtime"`
	Summary           store.Summary  `json:"summary"`
	LastRuns          []store.Run    `json:"last_runs"`
	EnvironmentKeys   []string       `json:"environment_keys"`
	ServerStartedAt   time.Time      `json:"server_started_at"`
	WorkingDirectory  string         `json:"working_directory"`
}

func NewServer(cfg config.Config, db *store.Store, collector *app.Collector) *Server {
	e := echo.New()
	e.Logger = slog.Default().With("component", "http")
	e.Use(middleware.Recover())
	e.Use(logx.GetEchoLogger(e.Logger))
	if cfg.Auth.Enable {
		e.Use(middleware.BasicAuth(func(_ *echo.Context, username, password string) (bool, error) {
			usernameOK := subtle.ConstantTimeCompare([]byte(username), []byte(cfg.Auth.Username)) == 1
			passwordOK := subtle.ConstantTimeCompare([]byte(password), []byte(cfg.Auth.Password)) == 1
			return usernameOK && passwordOK, nil
		}))
	}

	server := &Server{
		echo:           e,
		templates:      template.Must(template.ParseFS(assetFS, "templates/*.html")),
		cfg:            cfg,
		store:          db,
		collector:      collector,
		updateSignal:   make(chan struct{}),
		shutdownSignal: make(chan struct{}),
		startedAt:      time.Now().UTC(),
	}
	server.collector.SetOnChange(server.notifyDataChanged)

	staticFS, err := fs.Sub(assetFS, "static")
	if err != nil {
		panic(err)
	}
	e.StaticFS("/static", staticFS)

	e.GET("/", server.handleIndex)
	e.GET("/settings", server.handleSettingsPage)
	e.GET("/api/status/latest", server.handleLatestStatuses)
	e.GET("/api/friends/:friendSteamID/history", server.handleFriendHistory)
	e.GET("/api/runs", server.handleRuns)
	e.GET("/api/dashboard", server.handleDashboard)
	e.GET("/api/settings", server.handleGetSettings)
	e.PUT("/api/settings", server.handleUpdateSettings)
	e.GET("/api/system/status", server.handleSystemStatus)
	e.GET("/api/data/export", server.handleDataExport)
	e.POST("/api/data/import", server.handleDataImport)
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

func (s *Server) handleSettingsPage(c *echo.Context) error {
	payload := s.settingsPayload()
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	return s.templates.ExecuteTemplate(c.Response(), "settings.html", map[string]any{
		"Settings": payload,
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

func (s *Server) handleGetSettings(c *echo.Context) error {
	return c.JSON(http.StatusOK, s.settingsPayload())
}

func (s *Server) handleUpdateSettings(c *echo.Context) error {
	var editable config.EditableConfig
	if err := json.NewDecoder(c.Request().Body).Decode(&editable); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
	}
	if err := config.SaveEditable(s.cfg.ConfigPath, editable); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{
		"message":          "configuration saved to file",
		"config_path":      s.cfg.ConfigPath,
		"requires_restart": settingsRequireRestart(),
		"environment_keys": sortedKeys(s.cfg.EnvironmentOverrides()),
	})
}

func (s *Server) handleSystemStatus(c *echo.Context) error {
	status, err := s.systemStatusPayload()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, status)
}

func (s *Server) handleDataExport(c *echo.Context) error {
	format := strings.ToLower(c.QueryParam("format"))
	if format == "" {
		format = "json"
	}

	filename := "steam-watcher-export-" + time.Now().UTC().Format("20060102-150405")
	switch format {
	case "json":
		content, err := s.store.ExportJSON()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		c.Response().Header().Set(echo.HeaderContentDisposition, contentDisposition(filename+".json"))
		return c.Blob(http.StatusOK, echo.MIMEApplicationJSONCharsetUTF8, content)
	case "csv":
		content, err := s.store.ExportCSVZip()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		c.Response().Header().Set(echo.HeaderContentDisposition, contentDisposition(filename+".csv.zip"))
		return c.Blob(http.StatusOK, "application/zip", content)
	case "duckdb":
		content, err := s.store.ExportDuckDB()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		c.Response().Header().Set(echo.HeaderContentDisposition, contentDisposition(filename+".duckdb"))
		return c.Blob(http.StatusOK, "application/octet-stream", content)
	default:
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "format must be one of: json, csv, duckdb"})
	}
}

func (s *Server) handleDataImport(c *echo.Context) error {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "file is required"})
	}

	file, err := fileHeader.Open()
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	format := strings.ToLower(strings.TrimSpace(c.FormValue("format")))
	if format == "" {
		format = detectImportFormat(fileHeader.Filename)
	}

	var bundle store.ExportBundle
	switch format {
	case "json":
		bundle, err = s.store.ImportJSON(content)
	case "csv":
		bundle, err = s.store.ImportCSVZip(content)
	default:
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "format must be json or csv"})
	}
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	s.notifyDataChanged()
	return c.JSON(http.StatusOK, map[string]any{
		"message":        "data imported successfully",
		"format":         format,
		"runs":           len(bundle.Runs),
		"snapshots":      len(bundle.Snapshots),
		"imported_file":  fileHeader.Filename,
		"requires_merge": false,
	})
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

func (s *Server) settingsPayload() settingsPayload {
	return settingsPayload{
		Config:               s.cfg.Editable(),
		ConfigPath:           s.cfg.ConfigPath,
		EnvironmentOverrides: s.cfg.EnvironmentOverrides(),
		RequiresRestart:      settingsRequireRestart(),
	}
}

func (s *Server) systemStatusPayload() (systemStatusPayload, error) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	size, err := store.FileSize(s.cfg.DatabasePath)
	if err != nil && !os.IsNotExist(err) {
		return systemStatusPayload{}, err
	}

	summary, err := s.store.Summary()
	if err != nil {
		return systemStatusPayload{}, err
	}
	runs, err := s.store.RecentRuns(5)
	if err != nil {
		return systemStatusPayload{}, err
	}

	wd, _ := os.Getwd()
	lastGC := ""
	if mem.LastGC > 0 {
		lastGC = time.Unix(0, int64(mem.LastGC)).UTC().Format(time.RFC3339)
	}

	return systemStatusPayload{
		DatabasePath:      s.cfg.DatabasePath,
		DatabaseSizeBytes: size,
		ConfigPath:        s.cfg.ConfigPath,
		Collector:         s.collector.Status(),
		Runtime: runtimePayload{
			GoVersion:     runtime.Version(),
			Goroutines:    runtime.NumGoroutine(),
			GOMAXPROCS:    runtime.GOMAXPROCS(0),
			CPUCount:      runtime.NumCPU(),
			MemoryAlloc:   mem.Alloc,
			MemorySys:     mem.Sys,
			HeapAlloc:     mem.HeapAlloc,
			HeapObjects:   mem.HeapObjects,
			NumGC:         mem.NumGC,
			LastGCTime:    lastGC,
			UptimeSeconds: int64(time.Since(s.startedAt).Seconds()),
		},
		Summary:          summary,
		LastRuns:         runs,
		EnvironmentKeys:  sortedKeys(s.cfg.EnvironmentOverrides()),
		ServerStartedAt:  s.startedAt,
		WorkingDirectory: wd,
	}, nil
}

func settingsRequireRestart() []string {
	return []string{
		"listen_addr",
		"steam_api_key",
		"steam_id",
		"database_path",
		"collect_interval_seconds",
		"collect_on_start",
		"auth.enable",
		"auth.username",
		"auth.password",
	}
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func contentDisposition(filename string) string {
	return fmt.Sprintf("attachment; filename=%q", filepath.Base(filename))
}

func detectImportFormat(filename string) string {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".json"):
		return "json"
	case strings.HasSuffix(lower, ".csv.zip"), strings.HasSuffix(lower, ".zip"):
		return "csv"
	default:
		return ""
	}
}
