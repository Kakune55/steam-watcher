package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"steam-watcher/internal/store"
)

const demoOwnerSteamID = "demo-owner-steamid"

type demoFriend struct {
	SteamID    string
	Name       string
	ProfileURL string
	AvatarURL  string
}

type friendState struct {
	personaState int
	gameName     string
	gameAppID    int64
}

func main() {
	dbPath := flag.String("db", defaultDatabasePath(), "path to DuckDB database")
	flag.Parse()

	db, err := store.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := clearDemoData(db); err != nil {
		log.Fatal(err)
	}

	friends := demoFriends()
	if err := seedDemoData(db, friends, time.Now().UTC()); err != nil {
		log.Fatal(err)
	}

	log.Printf("seeded %d demo friends into %s", len(friends), *dbPath)
}

func defaultDatabasePath() string {
	if value := os.Getenv("DATABASE_PATH"); value != "" {
		return value
	}
	if value := os.Getenv("DUCKDB_PATH"); value != "" {
		return value
	}
	if value := databasePathFromConfig(); value != "" {
		return value
	}
	return "steam_status.duckdb"
}

func databasePathFromConfig() string {
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config.json"
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}

	var cfg struct {
		DatabasePath string `json:"database_path"`
	}
	if err := json.Unmarshal(content, &cfg); err != nil {
		return ""
	}
	if cfg.DatabasePath == "" {
		return ""
	}
	if filepath.IsAbs(cfg.DatabasePath) {
		return cfg.DatabasePath
	}

	baseDir := filepath.Dir(configPath)
	if baseDir == "." || baseDir == "" {
		return cfg.DatabasePath
	}
	return filepath.Join(baseDir, cfg.DatabasePath)
}

func clearDemoData(db *store.Store) error {
	if _, err := db.DB.Exec(`DELETE FROM friend_snapshots WHERE owner_steam_id = ?`, demoOwnerSteamID); err != nil {
		return err
	}
	if _, err := db.DB.Exec(`DELETE FROM collection_runs WHERE owner_steam_id = ?`, demoOwnerSteamID); err != nil {
		return err
	}
	return nil
}

func seedDemoData(db *store.Store, friends []demoFriend, now time.Time) error {
	lastActive := make(map[string]time.Time, len(friends))

	startDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(-1, 0, 0)
	endDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	for ts := startDay; ts.Before(endDay.AddDate(0, 0, -7)); ts = ts.Add(24 * time.Hour) {
		capturedAt := ts.Add(20 * time.Hour)
		if err := insertDemoRun(db, friends, capturedAt, lastActive); err != nil {
			return err
		}
	}

	weekStart := endDay.AddDate(0, 0, -7)
	for ts := weekStart; !ts.After(now); ts = ts.Add(time.Hour) {
		if err := insertDemoRun(db, friends, ts, lastActive); err != nil {
			return err
		}
	}

	return nil
}

func insertDemoRun(db *store.Store, friends []demoFriend, capturedAt time.Time, lastActive map[string]time.Time) error {
	runID := fmt.Sprintf("demo-%d", capturedAt.Unix())
	run := store.Run{
		RunID:       runID,
		OwnerSteam:  demoOwnerSteamID,
		StartedAt:   capturedAt.Add(-45 * time.Second),
		FinishedAt:  sql.NullTime{Time: capturedAt, Valid: true},
		FriendCount: len(friends),
		Fetched:     len(friends),
		Status:      "success",
	}
	if err := db.InsertRun(run); err != nil {
		return err
	}

	rows := make([]store.SnapshotRow, 0, len(friends))
	for idx, friend := range friends {
		state := demoStatus(idx, capturedAt)
		row := store.SnapshotRow{
			RunID:            runID,
			CapturedAt:       capturedAt,
			OwnerSteamID:     demoOwnerSteamID,
			FriendSteamID:    friend.SteamID,
			PersonaName:      friend.Name,
			PersonaState:     state.personaState,
			PersonaStateText: personaStateText(state.personaState),
			GameName:         nullString(state.gameName),
			GameAppID:        nullInt64(state.gameAppID),
			AvatarURL:        nullString(friend.AvatarURL),
			ProfileURL:       nullString(friend.ProfileURL),
			LastLogoffAt:     lastLogoff(lastActive, friend.SteamID, state, capturedAt),
		}
		rows = append(rows, row)
	}

	return db.InsertSnapshots(rows)
}

func demoFriends() []demoFriend {
	return []demoFriend{
		newDemoFriend("76561190000000001", "ice", "#4e8bd4", "#d7ecff"),
		newDemoFriend("76561190000000002", "风蕴雪月花", "#be5fd8", "#f4ddff"),
		newDemoFriend("76561190000000003", "白给长", "#59789a", "#e9f3ff"),
		newDemoFriend("76561190000000004", "灰烬航线", "#4d6c44", "#e7f5dc"),
		newDemoFriend("76561190000000005", "咸鱼骑士", "#866247", "#fff0de"),
		newDemoFriend("76561190000000006", "Luna", "#387d84", "#d8fbff"),
		newDemoFriend("76561190000000007", "NekoByte", "#7a587f", "#f7e0ff"),
		newDemoFriend("76561190000000008", "Aster", "#55616d", "#f1f6fb"),
	}
}

func newDemoFriend(steamID, name, bg, fg string) demoFriend {
	return demoFriend{
		SteamID:    steamID,
		Name:       name,
		ProfileURL: "https://steamcommunity.com/profiles/" + steamID,
		AvatarURL:  avatarDataURL(name, bg, fg),
	}
}

func avatarDataURL(name, bg, fg string) string {
	initial := strings.ToUpper(string([]rune(strings.TrimSpace(name))[0]))
	svg := fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="96" height="96" viewBox="0 0 96 96"><rect width="96" height="96" fill="%s"/><text x="50%%" y="54%%" dominant-baseline="middle" text-anchor="middle" font-family="Segoe UI, Arial, sans-serif" font-size="42" font-weight="700" fill="%s">%s</text></svg>`,
		bg, fg, initial,
	)
	return "data:image/svg+xml;utf8," + url.QueryEscape(svg)
}

func demoStatus(friendIndex int, capturedAt time.Time) friendState {
	hour := capturedAt.Hour()
	weekday := int(capturedAt.Weekday())
	dayOfYear := capturedAt.YearDay()

	switch friendIndex {
	case 0:
		if hour >= 20 && hour <= 23 {
			return game("Dishonored", 205100)
		}
		if hour >= 10 && hour <= 18 {
			return online()
		}
	case 1:
		if weekday == 5 || weekday == 6 {
			if hour >= 14 && hour <= 23 {
				return game("房产达人", 613100)
			}
		} else if hour >= 19 && hour <= 22 {
			return game("Stardew Valley", 413150)
		}
		if hour >= 12 && hour <= 18 {
			return away()
		}
	case 2:
		if dayOfYear%9 == 0 && hour >= 21 && hour <= 23 {
			return game("Slay the Spire", 646570)
		}
		if dayOfYear%3 == 0 && hour >= 18 && hour <= 21 {
			return online()
		}
	case 3:
		if weekday >= 1 && weekday <= 4 && hour >= 20 && hour <= 22 {
			return game("Apex Legends", 1172470)
		}
		if hour >= 9 && hour <= 17 {
			return away()
		}
	case 4:
		if hour >= 22 || hour <= 1 {
			return game("Balatro", 2379780)
		}
		if hour >= 14 && hour <= 18 {
			return online()
		}
	case 5:
		if dayOfYear%5 == 0 && hour >= 19 && hour <= 23 {
			return game("Hades", 1145360)
		}
		if hour >= 8 && hour <= 16 {
			return online()
		}
	case 6:
		if weekday == 0 && hour >= 13 && hour <= 23 {
			return game("雀魂麻将", 1329410)
		}
		if hour >= 18 && hour <= 20 {
			return online()
		}
	case 7:
		if dayOfYear%11 == 0 && hour >= 20 && hour <= 23 {
			return game("Cyberpunk 2077", 1091500)
		}
		if dayOfYear%2 == 0 && hour >= 11 && hour <= 12 {
			return online()
		}
	}

	return offline()
}

func online() friendState {
	return friendState{personaState: 1}
}

func away() friendState {
	return friendState{personaState: 3}
}

func offline() friendState {
	return friendState{personaState: 0}
}

func game(name string, appID int64) friendState {
	return friendState{personaState: 1, gameName: name, gameAppID: appID}
}

func personaStateText(state int) string {
	switch state {
	case 1:
		return "在线"
	case 3:
		return "离开"
	default:
		return "离线"
	}
}

func lastLogoff(lastActive map[string]time.Time, friendID string, state friendState, capturedAt time.Time) sql.NullTime {
	if state.personaState != 0 {
		lastActive[friendID] = capturedAt
		return sql.NullTime{}
	}
	if ts, ok := lastActive[friendID]; ok {
		return sql.NullTime{Time: ts, Valid: true}
	}
	return sql.NullTime{}
}

func nullString(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func nullInt64(value int64) sql.NullInt64 {
	if value == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: value, Valid: true}
}
