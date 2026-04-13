package app

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"steam-watcher/internal/config"
	"steam-watcher/internal/steam"
	"steam-watcher/internal/store"
)

type Collector struct {
	cfg    config.Config
	store  *store.Store
	client *steam.Client

	mu       sync.Mutex
	running  bool
	onChange func()
}

type CollectResult struct {
	RunID        string    `json:"run_id"`
	CapturedAt   time.Time `json:"captured_at"`
	OwnerSteamID string    `json:"owner_steam_id"`
	FriendCount  int       `json:"friend_count"`
	FetchedCount int       `json:"fetched_count"`
}

func NewCollector(cfg config.Config, db *store.Store) *Collector {
	return &Collector{
		cfg:    cfg,
		store:  db,
		client: steam.NewClient(cfg.SteamAPIKey),
	}
}

func (c *Collector) SetOnChange(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onChange = fn
}

func (c *Collector) StartScheduler(ctx context.Context) {
	ticker := time.NewTicker(c.cfg.CollectInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := c.CollectOnce(ctx); err != nil {
				slog.Error("scheduled collection failed", "error", err)
			}
		}
	}
}

func (c *Collector) CollectOnce(ctx context.Context) (*CollectResult, error) {
	if !c.startRun() {
		return nil, fmt.Errorf("collection already running")
	}
	defer c.finishRun()

	startedAt := time.Now().UTC()
	runID := fmt.Sprintf("%d", startedAt.UnixNano())

	ownerSteamID, err := c.client.ResolveSteamID64(ctx, c.cfg.SteamIDInput)
	if err != nil {
		return nil, err
	}

	run := store.Run{
		RunID:       runID,
		OwnerSteam:  ownerSteamID,
		StartedAt:   startedAt,
		FriendCount: 0,
		Fetched:     0,
		Status:      "running",
	}
	if err := c.store.InsertRun(run); err != nil {
		return nil, err
	}
	c.notifyChange()

	friendIDs, err := c.client.GetFriendIDs(ctx, ownerSteamID)
	if err != nil {
		_ = c.markRunFailed(run, err)
		return nil, err
	}

	players, err := c.client.GetPlayerSummaries(ctx, friendIDs)
	if err != nil {
		_ = c.markRunFailed(run, err)
		return nil, err
	}

	capturedAt := time.Now().UTC()
	snapshots := make([]store.SnapshotRow, 0, len(players))
	for _, player := range players {
		snapshot := store.SnapshotRow{
			RunID:            runID,
			CapturedAt:       capturedAt,
			OwnerSteamID:     ownerSteamID,
			FriendSteamID:    player.SteamID,
			PersonaName:      player.PersonaName,
			PersonaState:     player.PersonaState,
			PersonaStateText: steam.PersonaStateText(player.PersonaState),
			GameName:         nullString(player.GameExtra),
			GameAppID:        nullInt64(player.GameID),
			AvatarURL:        nullString(player.AvatarFull),
			ProfileURL:       nullString(player.ProfileURL),
			LastLogoffAt:     unixToNullTime(player.LastLogoff),
		}
		snapshots = append(snapshots, snapshot)
	}

	if err := c.store.InsertSnapshots(snapshots); err != nil {
		_ = c.markRunFailed(run, err)
		return nil, err
	}

	run.FinishedAt = sql.NullTime{Time: time.Now().UTC(), Valid: true}
	run.FriendCount = len(friendIDs)
	run.Fetched = len(players)
	run.Status = "success"
	if err := c.store.UpdateRun(run); err != nil {
		return nil, err
	}
	c.notifyChange()

	return &CollectResult{
		RunID:        runID,
		CapturedAt:   capturedAt,
		OwnerSteamID: ownerSteamID,
		FriendCount:  len(friendIDs),
		FetchedCount: len(players),
	}, nil
}

func (c *Collector) markRunFailed(run store.Run, runErr error) error {
	run.FinishedAt = sql.NullTime{Time: time.Now().UTC(), Valid: true}
	run.Status = "failed"
	run.Error = sql.NullString{String: runErr.Error(), Valid: true}
	if err := c.store.UpdateRun(run); err != nil {
		return err
	}
	c.notifyChange()
	return nil
}

func (c *Collector) startRun() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return false
	}
	c.running = true
	return true
}

func (c *Collector) finishRun() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.running = false
}

func (c *Collector) notifyChange() {
	c.mu.Lock()
	fn := c.onChange
	c.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func nullString(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func nullInt64(value string) sql.NullInt64 {
	if value == "" {
		return sql.NullInt64{}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: parsed, Valid: true}
}

func unixToNullTime(value int64) sql.NullTime {
	if value <= 0 {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: time.Unix(value, 0).UTC(), Valid: true}
}
