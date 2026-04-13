package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/jmoiron/sqlx"
)

type Store struct {
	DB *sqlx.DB
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

func Open(path string) (*Store, error) {
	db, err := sqlx.Open("duckdb", path)
	if err != nil {
		return nil, err
	}

	store := &Store{DB: db}
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
	RunID            string
	CapturedAt       time.Time
	OwnerSteamID     string
	FriendSteamID    string
	PersonaName      string
	PersonaState     int
	PersonaStateText string
	GameName         sql.NullString
	GameAppID        sql.NullInt64
	AvatarURL        sql.NullString
	ProfileURL       sql.NullString
	LastLogoffAt     sql.NullTime
}

func (s *Store) InsertSnapshots(rows []SnapshotRow) error {
	if len(rows) == 0 {
		return nil
	}

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
			ORDER BY captured_at
		`, friendSteamID, start, end)
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
