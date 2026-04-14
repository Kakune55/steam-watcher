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
	"strconv"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/jmoiron/sqlx"
)

type Store struct {
	DB *sqlx.DB
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

func (s *Store) Summary() (Summary, error) {
	var summary Summary
	err := s.DB.Get(&summary, `
		SELECT
			(SELECT COUNT(*) FROM collection_runs) AS run_count,
			(SELECT COUNT(*) FROM friend_snapshots) AS snapshot_count
	`)
	return summary, err
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

func (s *Store) ReplaceAllData(bundle ExportBundle) error {
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
