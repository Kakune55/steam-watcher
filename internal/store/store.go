package store

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/jmoiron/sqlx"
)

type Store struct {
	DB     *sqlx.DB
	dbPath string
	mu     sync.RWMutex
}

type Summary struct {
	RunCount      int64 `db:"run_count" json:"run_count"`
	SnapshotCount int64 `db:"snapshot_count" json:"snapshot_count"`
}

type Run struct {
	RunID       string         `db:"run_id" json:"run_id"`
	OwnerSteam  string         `db:"owner_steam_id" json:"owner_steam_id"`
	StartedAt   time.Time      `db:"started_at" json:"started_at"`
	FinishedAt  sql.NullTime   `db:"finished_at" json:"finished_at"`
	FriendCount int            `db:"friend_count" json:"friend_count"`
	Fetched     int            `db:"fetched_count" json:"fetched_count"`
	Status      string         `db:"status" json:"status"`
	Error       sql.NullString `db:"error_message" json:"error_message"`
}

type LatestFriendStatus struct {
	CapturedAt       time.Time      `db:"captured_at" json:"captured_at"`
	OwnerSteamID     string         `db:"owner_steam_id" json:"owner_steam_id"`
	FriendSteamID    string         `db:"friend_steam_id" json:"friend_steam_id"`
	PersonaName      string         `db:"persona_name" json:"persona_name"`
	PersonaState     int            `db:"persona_state" json:"persona_state"`
	PersonaStateText string         `db:"persona_state_text" json:"persona_state_text"`
	GameName         sql.NullString `db:"game_name" json:"game_name"`
	GameAppID        sql.NullInt64  `db:"game_app_id" json:"game_app_id"`
	AvatarURL        sql.NullString `db:"avatar_url" json:"avatar_url"`
	ProfileURL       sql.NullString `db:"profile_url" json:"profile_url"`
	LastLogoffAt     sql.NullTime   `db:"last_logoff_at" json:"last_logoff_at"`
	RunID            string         `db:"run_id" json:"run_id"`
}

type FriendHistoryMeta struct {
	FriendSteamID string         `db:"friend_steam_id" json:"friend_steam_id"`
	PersonaName   string         `db:"persona_name" json:"persona_name"`
	AvatarURL     sql.NullString `db:"avatar_url" json:"avatar_url"`
	ProfileURL    sql.NullString `db:"profile_url" json:"profile_url"`
}

type FriendHistoryPoint struct {
	BucketStart      time.Time      `db:"bucket_start" json:"bucket_start"`
	CapturedAt       time.Time      `db:"captured_at" json:"captured_at"`
	PersonaState     int            `db:"persona_state" json:"persona_state"`
	PersonaStateText string         `db:"persona_state_text" json:"persona_state_text"`
	GameName         sql.NullString `db:"game_name" json:"game_name"`
	GameAppID        sql.NullInt64  `db:"game_app_id" json:"game_app_id"`
}

type InsightFriend struct {
	FriendSteamID string        `json:"friend_steam_id"`
	PersonaName   string        `json:"persona_name"`
	AvatarURL     string        `json:"avatar_url"`
	ProfileURL    string        `json:"profile_url"`
	PlayMs        int64         `json:"play_ms"`
	TopGame       string        `json:"top_game"`
	TopGames      []InsightGame `json:"top_games"`
	HourBuckets   []int64       `json:"hour_buckets"`
}

type InsightGame struct {
	GameName    string `json:"game_name"`
	GameAppID   int64  `json:"game_app_id"`
	PlayerCount int    `json:"player_count"`
	PlayMs      int64  `json:"play_ms"`
}

type InsightCoopGame struct {
	GameName   string `json:"game_name"`
	GameAppID  int64  `json:"game_app_id"`
	Moments    int    `json:"moments"`
	MaxPlayers int    `json:"max_players"`
}

type InsightPeak struct {
	CapturedAt  time.Time `json:"captured_at"`
	PlayerCount int       `json:"player_count"`
}

type PlayInsights struct {
	Start             time.Time         `json:"start"`
	End               time.Time         `json:"end"`
	TotalPlayMs       int64             `json:"total_play_ms"`
	ActiveFriendCount int               `json:"active_friend_count"`
	TopPlayers        []InsightFriend   `json:"top_players"`
	PopularGames      []InsightGame     `json:"popular_games"`
	CoopGames         []InsightCoopGame `json:"coop_games"`
	Peak              InsightPeak       `json:"peak"`
	HourBuckets       []int64           `json:"hour_buckets"`
}

type ExportBundle struct {
	ExportedAt time.Time     `json:"exported_at"`
	Runs       []Run         `json:"runs"`
	Snapshots  []SnapshotRow `json:"snapshots"`
}

func Open(path string) (*Store, error) {
	db, err := sqlx.Open("duckdb", path)
	if err != nil {
		return nil, err
	}

	store := &Store{
		DB:     db,
		dbPath: path,
	}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *Store) Close() error {
	return s.DB.Close()
}

func (s *Store) migrate() error {
	schema := []string{
		`
		CREATE TABLE IF NOT EXISTS collection_runs (
			run_id VARCHAR PRIMARY KEY,
			owner_steam_id VARCHAR NOT NULL,
			started_at TIMESTAMP NOT NULL,
			finished_at TIMESTAMP,
			friend_count INTEGER NOT NULL DEFAULT 0,
			fetched_count INTEGER NOT NULL DEFAULT 0,
			status VARCHAR NOT NULL,
			error_message VARCHAR
		)
		`,
		`
		CREATE TABLE IF NOT EXISTS friend_snapshots (
			run_id VARCHAR NOT NULL,
			captured_at TIMESTAMP NOT NULL,
			owner_steam_id VARCHAR NOT NULL,
			friend_steam_id VARCHAR NOT NULL,
			persona_name VARCHAR,
			persona_state INTEGER,
			persona_state_text VARCHAR,
			game_name VARCHAR,
			game_app_id BIGINT,
			avatar_url VARCHAR,
			profile_url VARCHAR,
			last_logoff_at TIMESTAMP
		)
		`,
		`ALTER TABLE friend_snapshots ADD COLUMN IF NOT EXISTS avatar_url VARCHAR`,
		`CREATE INDEX IF NOT EXISTS idx_collection_runs_finished_at ON collection_runs (finished_at)`,
		`CREATE INDEX IF NOT EXISTS idx_friend_snapshots_run_id ON friend_snapshots (run_id)`,
	}

	for _, stmt := range schema {
		if _, err := s.DB.Exec(stmt); err != nil {
			return fmt.Errorf("migrate failed: %w", err)
		}
	}

	return nil
}
func (s *Store) InsertRun(run Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.DB.NamedExec(`
		INSERT INTO collection_runs (
			run_id, owner_steam_id, started_at, finished_at, friend_count, fetched_count, status, error_message
		) VALUES (
			:run_id, :owner_steam_id, :started_at, :finished_at, :friend_count, :fetched_count, :status, :error_message
		)
	`, run)
	return err
}

func (s *Store) UpdateRun(run Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.DB.NamedExec(`
		UPDATE collection_runs
		SET finished_at = :finished_at,
			friend_count = :friend_count,
			fetched_count = :fetched_count,
			status = :status,
			error_message = :error_message
		WHERE run_id = :run_id
	`, run)
	return err
}

type SnapshotRow struct {
	RunID            string         `db:"run_id" json:"run_id"`
	CapturedAt       time.Time      `db:"captured_at" json:"captured_at"`
	OwnerSteamID     string         `db:"owner_steam_id" json:"owner_steam_id"`
	FriendSteamID    string         `db:"friend_steam_id" json:"friend_steam_id"`
	PersonaName      string         `db:"persona_name" json:"persona_name"`
	PersonaState     int            `db:"persona_state" json:"persona_state"`
	PersonaStateText string         `db:"persona_state_text" json:"persona_state_text"`
	GameName         sql.NullString `db:"game_name" json:"game_name"`
	GameAppID        sql.NullInt64  `db:"game_app_id" json:"game_app_id"`
	AvatarURL        sql.NullString `db:"avatar_url" json:"avatar_url"`
	ProfileURL       sql.NullString `db:"profile_url" json:"profile_url"`
	LastLogoffAt     sql.NullTime   `db:"last_logoff_at" json:"last_logoff_at"`
}

func (s *Store) InsertSnapshots(rows []SnapshotRow) error {
	if len(rows) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.DB.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Preparex(`
		INSERT INTO friend_snapshots (
			run_id, captured_at, owner_steam_id, friend_steam_id, persona_name, persona_state,
			persona_state_text, game_name, game_app_id, avatar_url, profile_url, last_logoff_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, row := range rows {
		if _, err := stmt.Exec(
			row.RunID,
			row.CapturedAt,
			row.OwnerSteamID,
			row.FriendSteamID,
			row.PersonaName,
			row.PersonaState,
			row.PersonaStateText,
			row.GameName,
			row.GameAppID,
			row.AvatarURL,
			row.ProfileURL,
			row.LastLogoffAt,
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) LatestStatuses() ([]LatestFriendStatus, error) {
	rows := make([]LatestFriendStatus, 0)
	err := s.DB.Select(&rows, `
		WITH latest_run AS (
			SELECT run_id
			FROM collection_runs
			WHERE status = 'success'
			ORDER BY finished_at DESC
			LIMIT 1
		)
		SELECT
			fs.run_id,
			fs.captured_at,
			fs.owner_steam_id,
			fs.friend_steam_id,
			fs.persona_name,
			fs.persona_state,
			fs.persona_state_text,
			fs.game_name,
			fs.game_app_id,
			fs.avatar_url,
			fs.profile_url,
			fs.last_logoff_at
		FROM friend_snapshots fs
		INNER JOIN latest_run lr ON lr.run_id = fs.run_id
		ORDER BY lower(fs.persona_name), fs.friend_steam_id
	`)
	return rows, err
}

func (s *Store) RecentRuns(limit int) ([]Run, error) {
	rows := make([]Run, 0)
	err := s.DB.Select(&rows, `
		SELECT run_id, owner_steam_id, started_at, finished_at, friend_count, fetched_count, status, error_message
		FROM collection_runs
		ORDER BY started_at DESC
		LIMIT ?
	`, limit)
	return rows, err
}

func (s *Store) FriendHistoryMeta(friendSteamID string) (FriendHistoryMeta, error) {
	var meta FriendHistoryMeta
	err := s.DB.Get(&meta, `
		SELECT friend_steam_id, persona_name, avatar_url, profile_url
		FROM friend_snapshots
		WHERE friend_steam_id = ?
		ORDER BY captured_at DESC
		LIMIT 1
	`, friendSteamID)
	return meta, err
}

func (s *Store) FriendHistory(friendSteamID string, start, end time.Time, bucketUnit string, tzOffsetMinutes int) ([]FriendHistoryPoint, error) {
	if bucketUnit == "raw" {
		rows := make([]FriendHistoryPoint, 0)
		err := s.DB.Select(&rows, `
			WITH boundary_point AS (
				SELECT
					captured_at AS bucket_start,
					captured_at,
					persona_state,
					persona_state_text,
					game_name,
					game_app_id
				FROM friend_snapshots
				WHERE friend_steam_id = ?
				  AND captured_at < ?
				ORDER BY captured_at DESC
				LIMIT 1
			),
			range_points AS (
				SELECT
					captured_at AS bucket_start,
					captured_at,
					persona_state,
					persona_state_text,
					game_name,
					game_app_id
				FROM friend_snapshots
				WHERE friend_steam_id = ?
				  AND captured_at >= ?
				  AND captured_at < ?
			)
			SELECT *
			FROM (
				SELECT * FROM boundary_point
				UNION ALL
				SELECT * FROM range_points
			)
			ORDER BY captured_at
		`, friendSteamID, start, friendSteamID, start, end)
		return rows, err
	}

	offsetExpr := fmt.Sprintf("INTERVAL %d MINUTE", tzOffsetMinutes)
	bucketExpr := fmt.Sprintf("(date_trunc('day', captured_at + %s) - %s)", offsetExpr, offsetExpr)
	if bucketUnit == "hour" {
		bucketExpr = fmt.Sprintf("(date_trunc('hour', captured_at + %s) - %s)", offsetExpr, offsetExpr)
	}

	query := fmt.Sprintf(`
		WITH ranked AS (
			SELECT
				%s AS bucket_start,
				captured_at,
				persona_state,
				persona_state_text,
				game_name,
				game_app_id,
				row_number() OVER (
					PARTITION BY %s
					ORDER BY captured_at DESC
				) AS rn
			FROM friend_snapshots
			WHERE friend_steam_id = ?
			  AND captured_at >= ?
			  AND captured_at < ?
		)
		SELECT
			bucket_start,
			captured_at,
			persona_state,
			persona_state_text,
			game_name,
			game_app_id
		FROM ranked
		WHERE rn = 1
		ORDER BY bucket_start
	`, bucketExpr, bucketExpr)

	rows := make([]FriendHistoryPoint, 0)
	err := s.DB.Select(&rows, query, friendSteamID, start, end)
	return rows, err
}

func (s *Store) PlayInsights(start, end time.Time, tzOffsetMinutes int) (PlayInsights, error) {
	rows := make([]SnapshotRow, 0)
	err := s.DB.Select(&rows, `
		WITH boundary_ranked AS (
			SELECT
				run_id,
				captured_at,
				owner_steam_id,
				friend_steam_id,
				persona_name,
				persona_state,
				persona_state_text,
				game_name,
				game_app_id,
				avatar_url,
				profile_url,
				last_logoff_at,
				row_number() OVER (
					PARTITION BY friend_steam_id
					ORDER BY captured_at DESC
				) AS rn
			FROM friend_snapshots
			WHERE captured_at < ?
		),
		boundary_points AS (
			SELECT
				run_id,
				captured_at,
				owner_steam_id,
				friend_steam_id,
				persona_name,
				persona_state,
				persona_state_text,
				game_name,
				game_app_id,
				avatar_url,
				profile_url,
				last_logoff_at
			FROM boundary_ranked
			WHERE rn = 1
		),
		range_points AS (
			SELECT
				run_id,
				captured_at,
				owner_steam_id,
				friend_steam_id,
				persona_name,
				persona_state,
				persona_state_text,
				game_name,
				game_app_id,
				avatar_url,
				profile_url,
				last_logoff_at
			FROM friend_snapshots
			WHERE captured_at >= ?
			  AND captured_at < ?
		)
		SELECT *
		FROM (
			SELECT * FROM boundary_points
			UNION ALL
			SELECT * FROM range_points
		)
		ORDER BY friend_steam_id ASC, captured_at ASC
	`, start, start, end)
	if err != nil {
		return PlayInsights{}, err
	}

	return buildPlayInsights(rows, start, end, tzOffsetMinutes), nil
}

func (s *Store) Summary() (Summary, error) {
	var summary Summary
	err := s.DB.Get(&summary, `
		SELECT
			(SELECT COUNT(*) FROM collection_runs) AS run_count,
			(SELECT COUNT(*) FROM friend_snapshots) AS snapshot_count
	`)
	return summary, err
}

type insightFriendAgg struct {
	id          string
	name        string
	avatarURL   string
	profileURL  string
	playMs      int64
	games       map[string]int64
	hourBuckets []int64
}

type insightGameAgg struct {
	name    string
	appID   int64
	playMs  int64
	players map[string]struct{}
}

type insightCoopAgg struct {
	name       string
	appID      int64
	moments    int
	maxPlayers int
}

func buildPlayInsights(rows []SnapshotRow, start, end time.Time, tzOffsetMinutes int) PlayInsights {
	const maxSegment = 2 * time.Hour

	insights := PlayInsights{
		Start:       start,
		End:         end,
		HourBuckets: make([]int64, 24),
	}
	if !end.After(start) {
		return insights
	}

	byFriend := make(map[string][]SnapshotRow)
	rangeRows := make([]SnapshotRow, 0)
	for _, row := range rows {
		byFriend[row.FriendSteamID] = append(byFriend[row.FriendSteamID], row)
		if !row.CapturedAt.Before(start) && row.CapturedAt.Before(end) {
			rangeRows = append(rangeRows, row)
		}
	}

	friends := make(map[string]*insightFriendAgg)
	games := make(map[string]*insightGameAgg)
	for friendID, friendRows := range byFriend {
		sort.Slice(friendRows, func(i, j int) bool {
			return friendRows[i].CapturedAt.Before(friendRows[j].CapturedAt)
		})

		for i, row := range friendRows {
			if !row.GameName.Valid || row.GameName.String == "" {
				continue
			}

			segmentStart := maxTime(row.CapturedAt, start)
			segmentEnd := end
			if i+1 < len(friendRows) {
				segmentEnd = minTime(friendRows[i+1].CapturedAt, end)
			}
			if !segmentEnd.After(segmentStart) {
				continue
			}
			if segmentEnd.Sub(segmentStart) > maxSegment {
				segmentEnd = segmentStart.Add(maxSegment)
			}

			durationMs := segmentEnd.Sub(segmentStart).Milliseconds()
			gameKey := insightGameKey(row.GameName.String, row.GameAppID)
			friend := getInsightFriend(friends, row)
			game := getInsightGame(games, row)

			friend.playMs += durationMs
			friend.games[gameKey] += durationMs
			addInsightHourBuckets(friend.hourBuckets, segmentStart, segmentEnd, tzOffsetMinutes)
			game.playMs += durationMs
			game.players[friendID] = struct{}{}
			insights.TotalPlayMs += durationMs
			addInsightHourBuckets(insights.HourBuckets, segmentStart, segmentEnd, tzOffsetMinutes)
		}
	}

	insights.TopPlayers = topInsightFriends(friends, 5)
	insights.PopularGames = topInsightGames(games, 5)
	insights.ActiveFriendCount = activeInsightFriendCount(friends)
	insights.Peak = insightPeak(rangeRows)
	insights.CoopGames = topInsightCoopGames(rangeRows, 5)
	return insights
}

func getInsightFriend(items map[string]*insightFriendAgg, row SnapshotRow) *insightFriendAgg {
	item := items[row.FriendSteamID]
	if item == nil {
		item = &insightFriendAgg{
			id:          row.FriendSteamID,
			games:       make(map[string]int64),
			hourBuckets: make([]int64, 24),
		}
		items[row.FriendSteamID] = item
	}
	item.name = row.PersonaName
	if row.AvatarURL.Valid {
		item.avatarURL = row.AvatarURL.String
	}
	if row.ProfileURL.Valid {
		item.profileURL = row.ProfileURL.String
	}
	return item
}

func getInsightGame(items map[string]*insightGameAgg, row SnapshotRow) *insightGameAgg {
	key := insightGameKey(row.GameName.String, row.GameAppID)
	item := items[key]
	if item == nil {
		item = &insightGameAgg{
			name:    row.GameName.String,
			appID:   nullInt64Value(row.GameAppID),
			players: make(map[string]struct{}),
		}
		items[key] = item
	}
	return item
}

func topInsightFriends(items map[string]*insightFriendAgg, limit int) []InsightFriend {
	rows := make([]InsightFriend, 0, len(items))
	for _, item := range items {
		if item.playMs <= 0 {
			continue
		}
		topGame := ""
		var topGameMs int64
		for game, playMs := range item.games {
			if playMs > topGameMs || (playMs == topGameMs && game < topGame) {
				topGame = game
				topGameMs = playMs
			}
		}
		rows = append(rows, InsightFriend{
			FriendSteamID: item.id,
			PersonaName:   item.name,
			AvatarURL:     item.avatarURL,
			ProfileURL:    item.profileURL,
			PlayMs:        item.playMs,
			TopGame:       insightGameName(topGame),
			TopGames:      topFriendGames(item.games, 5),
			HourBuckets:   append([]int64(nil), item.hourBuckets...),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].PlayMs != rows[j].PlayMs {
			return rows[i].PlayMs > rows[j].PlayMs
		}
		return rows[i].PersonaName < rows[j].PersonaName
	})
	return limitInsightFriends(rows, limit)
}

func topInsightGames(items map[string]*insightGameAgg, limit int) []InsightGame {
	rows := make([]InsightGame, 0, len(items))
	for _, item := range items {
		if item.playMs <= 0 {
			continue
		}
		rows = append(rows, InsightGame{
			GameName:    item.name,
			GameAppID:   item.appID,
			PlayerCount: len(item.players),
			PlayMs:      item.playMs,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].PlayerCount != rows[j].PlayerCount {
			return rows[i].PlayerCount > rows[j].PlayerCount
		}
		if rows[i].PlayMs != rows[j].PlayMs {
			return rows[i].PlayMs > rows[j].PlayMs
		}
		return rows[i].GameName < rows[j].GameName
	})
	return limitInsightGames(rows, limit)
}

func topFriendGames(items map[string]int64, limit int) []InsightGame {
	rows := make([]InsightGame, 0, len(items))
	for key, playMs := range items {
		if playMs <= 0 {
			continue
		}
		name, appID := insightGameParts(key)
		rows = append(rows, InsightGame{
			GameName:    name,
			GameAppID:   appID,
			PlayerCount: 1,
			PlayMs:      playMs,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].PlayMs != rows[j].PlayMs {
			return rows[i].PlayMs > rows[j].PlayMs
		}
		return rows[i].GameName < rows[j].GameName
	})
	return limitInsightGames(rows, limit)
}

func insightPeak(rows []SnapshotRow) InsightPeak {
	type peakBucket struct {
		capturedAt time.Time
		players    map[string]struct{}
	}
	buckets := make(map[int64]*peakBucket)
	for _, row := range rows {
		if !row.GameName.Valid || row.GameName.String == "" {
			continue
		}
		key := row.CapturedAt.UnixNano()
		bucket := buckets[key]
		if bucket == nil {
			bucket = &peakBucket{
				capturedAt: row.CapturedAt,
				players:    make(map[string]struct{}),
			}
			buckets[key] = bucket
		}
		bucket.players[row.FriendSteamID] = struct{}{}
	}

	var peak InsightPeak
	for _, bucket := range buckets {
		count := len(bucket.players)
		if count > peak.PlayerCount || (count == peak.PlayerCount && (peak.CapturedAt.IsZero() || bucket.capturedAt.Before(peak.CapturedAt))) {
			peak = InsightPeak{CapturedAt: bucket.capturedAt, PlayerCount: count}
		}
	}
	return peak
}

func topInsightCoopGames(rows []SnapshotRow, limit int) []InsightCoopGame {
	type coopMoment struct {
		name    string
		appID   int64
		players map[string]struct{}
	}

	moments := make(map[string]*coopMoment)
	for _, row := range rows {
		if !row.GameName.Valid || row.GameName.String == "" {
			continue
		}
		key := strconv.FormatInt(row.CapturedAt.UnixNano(), 10) + "|" + insightGameKey(row.GameName.String, row.GameAppID)
		moment := moments[key]
		if moment == nil {
			moment = &coopMoment{
				name:    row.GameName.String,
				appID:   nullInt64Value(row.GameAppID),
				players: make(map[string]struct{}),
			}
			moments[key] = moment
		}
		moment.players[row.FriendSteamID] = struct{}{}
	}

	aggregates := make(map[string]*insightCoopAgg)
	for _, moment := range moments {
		count := len(moment.players)
		if count < 2 {
			continue
		}
		key := insightGameKeyFromValues(moment.name, moment.appID)
		agg := aggregates[key]
		if agg == nil {
			agg = &insightCoopAgg{name: moment.name, appID: moment.appID}
			aggregates[key] = agg
		}
		agg.moments += 1
		if count > agg.maxPlayers {
			agg.maxPlayers = count
		}
	}

	result := make([]InsightCoopGame, 0, len(aggregates))
	for _, agg := range aggregates {
		result = append(result, InsightCoopGame{
			GameName:   agg.name,
			GameAppID:  agg.appID,
			Moments:    agg.moments,
			MaxPlayers: agg.maxPlayers,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Moments != result[j].Moments {
			return result[i].Moments > result[j].Moments
		}
		if result[i].MaxPlayers != result[j].MaxPlayers {
			return result[i].MaxPlayers > result[j].MaxPlayers
		}
		return result[i].GameName < result[j].GameName
	})
	return limitInsightCoopGames(result, limit)
}

func addInsightHourBuckets(buckets []int64, start, end time.Time, tzOffsetMinutes int) {
	offset := time.Duration(tzOffsetMinutes) * time.Minute
	for cursor := start; cursor.Before(end); {
		local := cursor.Add(offset)
		nextLocalHour := time.Date(local.Year(), local.Month(), local.Day(), local.Hour()+1, 0, 0, 0, time.UTC)
		next := minTime(nextLocalHour.Add(-offset), end)
		buckets[local.Hour()] += next.Sub(cursor).Milliseconds()
		cursor = next
	}
}

func insightGameKey(name string, appID sql.NullInt64) string {
	return insightGameKeyFromValues(name, nullInt64Value(appID))
}

func insightGameKeyFromValues(name string, appID int64) string {
	if appID > 0 {
		return strconv.FormatInt(appID, 10) + "|" + name
	}
	return "name|" + name
}

func insightGameName(key string) string {
	parts := strings.SplitN(key, "|", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return key
}

func insightGameParts(key string) (string, int64) {
	parts := strings.SplitN(key, "|", 2)
	if len(parts) != 2 {
		return key, 0
	}
	appID, _ := strconv.ParseInt(parts[0], 10, 64)
	return parts[1], appID
}

func nullInt64Value(value sql.NullInt64) int64 {
	if value.Valid {
		return value.Int64
	}
	return 0
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func limitInsightFriends(items []InsightFriend, limit int) []InsightFriend {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitInsightGames(items []InsightGame, limit int) []InsightGame {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitInsightCoopGames(items []InsightCoopGame, limit int) []InsightCoopGame {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}

func activeInsightFriendCount(items map[string]*insightFriendAgg) int {
	count := 0
	for _, item := range items {
		if item.playMs > 0 {
			count += 1
		}
	}
	return count
}

func (s *Store) AllRuns() ([]Run, error) {
	rows := make([]Run, 0)
	err := s.DB.Select(&rows, `
		SELECT run_id, owner_steam_id, started_at, finished_at, friend_count, fetched_count, status, error_message
		FROM collection_runs
		ORDER BY started_at ASC
	`)
	return rows, err
}

func (s *Store) AllSnapshots() ([]SnapshotRow, error) {
	rows := make([]SnapshotRow, 0)
	err := s.DB.Select(&rows, `
		SELECT
			run_id,
			captured_at,
			owner_steam_id,
			friend_steam_id,
			persona_name,
			persona_state,
			persona_state_text,
			game_name,
			game_app_id,
			avatar_url,
			profile_url,
			last_logoff_at
		FROM friend_snapshots
		ORDER BY captured_at ASC, friend_steam_id ASC
	`)
	return rows, err
}

func (s *Store) ExportBundle() (ExportBundle, error) {
	runs, err := s.AllRuns()
	if err != nil {
		return ExportBundle{}, err
	}
	snapshots, err := s.AllSnapshots()
	if err != nil {
		return ExportBundle{}, err
	}
	return ExportBundle{
		ExportedAt: time.Now().UTC(),
		Runs:       runs,
		Snapshots:  snapshots,
	}, nil
}

func (s *Store) ExportDuckDB() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.DB.Exec(`CHECKPOINT`); err != nil {
		return nil, fmt.Errorf("checkpoint database: %w", err)
	}

	content, err := os.ReadFile(s.dbPath)
	if err != nil {
		return nil, err
	}
	return content, nil
}

func (s *Store) ReplaceAllData(bundle ExportBundle) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.DB.BeginTxx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM friend_snapshots`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM collection_runs`); err != nil {
		return err
	}

	for _, run := range bundle.Runs {
		if _, err := tx.NamedExec(`
			INSERT INTO collection_runs (
				run_id, owner_steam_id, started_at, finished_at, friend_count, fetched_count, status, error_message
			) VALUES (
				:run_id, :owner_steam_id, :started_at, :finished_at, :friend_count, :fetched_count, :status, :error_message
			)
		`, run); err != nil {
			return err
		}
	}

	if len(bundle.Snapshots) > 0 {
		stmt, err := tx.Preparex(`
			INSERT INTO friend_snapshots (
				run_id, captured_at, owner_steam_id, friend_steam_id, persona_name, persona_state,
				persona_state_text, game_name, game_app_id, avatar_url, profile_url, last_logoff_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		for _, row := range bundle.Snapshots {
			if _, err := stmt.Exec(
				row.RunID,
				row.CapturedAt,
				row.OwnerSteamID,
				row.FriendSteamID,
				row.PersonaName,
				row.PersonaState,
				row.PersonaStateText,
				row.GameName,
				row.GameAppID,
				row.AvatarURL,
				row.ProfileURL,
				row.LastLogoffAt,
			); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

func (s *Store) ExportJSON() ([]byte, error) {
	bundle, err := s.ExportBundle()
	if err != nil {
		return nil, err
	}
	content, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return nil, err
	}
	content = append(content, '\n')
	return content, nil
}

func (s *Store) ImportJSON(data []byte) (ExportBundle, error) {
	var bundle ExportBundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		return ExportBundle{}, fmt.Errorf("parse json backup: %w", err)
	}
	if err := s.ReplaceAllData(bundle); err != nil {
		return ExportBundle{}, err
	}
	return bundle, nil
}

func (s *Store) ExportCSVZip() ([]byte, error) {
	bundle, err := s.ExportBundle()
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	if err := writeRunsCSV(zw, bundle.Runs); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := writeSnapshotsCSV(zw, bundle.Snapshots); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (s *Store) ImportCSVZip(data []byte) (ExportBundle, error) {
	readerAt := bytes.NewReader(data)
	zr, err := zip.NewReader(readerAt, int64(len(data)))
	if err != nil {
		return ExportBundle{}, fmt.Errorf("parse zip backup: %w", err)
	}

	var bundle ExportBundle
	for _, file := range zr.File {
		rc, err := file.Open()
		if err != nil {
			return ExportBundle{}, err
		}

		switch file.Name {
		case "collection_runs.csv":
			bundle.Runs, err = readRunsCSV(rc)
		case "friend_snapshots.csv":
			bundle.Snapshots, err = readSnapshotsCSV(rc)
		}
		_ = rc.Close()
		if err != nil {
			return ExportBundle{}, err
		}
	}

	if err := s.ReplaceAllData(bundle); err != nil {
		return ExportBundle{}, err
	}
	return bundle, nil
}

func FileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func writeRunsCSV(zw *zip.Writer, runs []Run) error {
	w, err := zw.Create("collection_runs.csv")
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"run_id", "owner_steam_id", "started_at", "finished_at", "friend_count", "fetched_count", "status", "error_message"}); err != nil {
		return err
	}
	for _, run := range runs {
		record := []string{
			run.RunID,
			run.OwnerSteam,
			run.StartedAt.Format(time.RFC3339Nano),
			nullTimeString(run.FinishedAt),
			strconv.Itoa(run.FriendCount),
			strconv.Itoa(run.Fetched),
			run.Status,
			nullStringString(run.Error),
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func writeSnapshotsCSV(zw *zip.Writer, rows []SnapshotRow) error {
	w, err := zw.Create("friend_snapshots.csv")
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"run_id", "captured_at", "owner_steam_id", "friend_steam_id", "persona_name", "persona_state", "persona_state_text", "game_name", "game_app_id", "avatar_url", "profile_url", "last_logoff_at"}); err != nil {
		return err
	}
	for _, row := range rows {
		record := []string{
			row.RunID,
			row.CapturedAt.Format(time.RFC3339Nano),
			row.OwnerSteamID,
			row.FriendSteamID,
			row.PersonaName,
			strconv.Itoa(row.PersonaState),
			row.PersonaStateText,
			nullStringString(row.GameName),
			nullInt64String(row.GameAppID),
			nullStringString(row.AvatarURL),
			nullStringString(row.ProfileURL),
			nullTimeString(row.LastLogoffAt),
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func readRunsCSV(r io.Reader) ([]Run, error) {
	cr := csv.NewReader(r)
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	out := make([]Run, 0, max(len(rows)-1, 0))
	for idx, record := range rows {
		if idx == 0 {
			continue
		}
		startedAt, err := time.Parse(time.RFC3339Nano, record[2])
		if err != nil {
			return nil, fmt.Errorf("parse collection_runs row %d started_at: %w", idx+1, err)
		}
		friendCount, err := strconv.Atoi(record[4])
		if err != nil {
			return nil, fmt.Errorf("parse collection_runs row %d friend_count: %w", idx+1, err)
		}
		fetchedCount, err := strconv.Atoi(record[5])
		if err != nil {
			return nil, fmt.Errorf("parse collection_runs row %d fetched_count: %w", idx+1, err)
		}
		out = append(out, Run{
			RunID:       record[0],
			OwnerSteam:  record[1],
			StartedAt:   startedAt,
			FinishedAt:  stringToNullTime(record[3]),
			FriendCount: friendCount,
			Fetched:     fetchedCount,
			Status:      record[6],
			Error:       stringToNullString(record[7]),
		})
	}
	return out, nil
}

func readSnapshotsCSV(r io.Reader) ([]SnapshotRow, error) {
	cr := csv.NewReader(r)
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	out := make([]SnapshotRow, 0, max(len(rows)-1, 0))
	for idx, record := range rows {
		if idx == 0 {
			continue
		}
		capturedAt, err := time.Parse(time.RFC3339Nano, record[1])
		if err != nil {
			return nil, fmt.Errorf("parse friend_snapshots row %d captured_at: %w", idx+1, err)
		}
		personaState, err := strconv.Atoi(record[5])
		if err != nil {
			return nil, fmt.Errorf("parse friend_snapshots row %d persona_state: %w", idx+1, err)
		}
		out = append(out, SnapshotRow{
			RunID:            record[0],
			CapturedAt:       capturedAt,
			OwnerSteamID:     record[2],
			FriendSteamID:    record[3],
			PersonaName:      record[4],
			PersonaState:     personaState,
			PersonaStateText: record[6],
			GameName:         stringToNullString(record[7]),
			GameAppID:        stringToNullInt64(record[8]),
			AvatarURL:        stringToNullString(record[9]),
			ProfileURL:       stringToNullString(record[10]),
			LastLogoffAt:     stringToNullTime(record[11]),
		})
	}
	return out, nil
}

func nullStringString(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func nullInt64String(value sql.NullInt64) string {
	if !value.Valid {
		return ""
	}
	return strconv.FormatInt(value.Int64, 10)
}

func nullTimeString(value sql.NullTime) string {
	if !value.Valid {
		return ""
	}
	return value.Time.Format(time.RFC3339Nano)
}

func stringToNullString(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func stringToNullInt64(value string) sql.NullInt64 {
	if value == "" {
		return sql.NullInt64{}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: parsed, Valid: true}
}

func stringToNullTime(value string) sql.NullTime {
	if value == "" {
		return sql.NullTime{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: parsed, Valid: true}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
