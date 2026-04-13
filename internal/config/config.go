package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Config struct {
	ConfigPath      string `json:"-"`
	ListenAddr      string
	SteamAPIKey     string
	SteamIDInput    string
	DatabasePath    string
	CollectInterval time.Duration
	CollectOnStart  bool
}

type fileConfig struct {
	ListenAddr             string `json:"listen_addr"`
	SteamAPIKey            string `json:"steam_api_key"`
	SteamIDInput           string `json:"steam_id"`
	DatabasePath           string `json:"database_path"`
	CollectIntervalSeconds int    `json:"collect_interval_seconds"`
	CollectOnStart         *bool  `json:"collect_on_start"`
}

func Load() (Config, error) {
	configPath := getEnv("CONFIG_PATH", "config.json")

	cfg := Config{
		ConfigPath:      configPath,
		ListenAddr:      getEnv("APP_ADDR", ":8080"),
		SteamAPIKey:     os.Getenv("STEAM_API_KEY"),
		SteamIDInput:    os.Getenv("STEAM_ID64"),
		DatabasePath:    getEnvAny([]string{"DATABASE_PATH", "DUCKDB_PATH"}, "steam_status.duckdb"),
		CollectInterval: time.Duration(getEnvInt("COLLECT_INTERVAL_SECONDS", 300)) * time.Second,
		CollectOnStart:  getEnvBool("COLLECT_ON_START", true),
	}

	if err := applyFileConfig(&cfg, configPath); err != nil {
		return Config{}, err
	}

	if cfg.SteamAPIKey == "" {
		return Config{}, fmt.Errorf("missing Steam API key: set steam_api_key in %s or STEAM_API_KEY in env", configPath)
	}
	if cfg.SteamIDInput == "" {
		return Config{}, fmt.Errorf("missing Steam ID: set steam_id in %s or STEAM_ID64 in env", configPath)
	}
	if cfg.CollectInterval <= 0 {
		return Config{}, fmt.Errorf("COLLECT_INTERVAL_SECONDS must be greater than 0")
	}

	return cfg, nil
}

func applyFileConfig(cfg *Config, path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read config file %s: %w", path, err)
	}

	var fileCfg fileConfig
	if err := json.Unmarshal(content, &fileCfg); err != nil {
		return fmt.Errorf("parse config file %s: %w", path, err)
	}

	if fileCfg.ListenAddr != "" && os.Getenv("APP_ADDR") == "" {
		cfg.ListenAddr = fileCfg.ListenAddr
	}
	if fileCfg.SteamAPIKey != "" && os.Getenv("STEAM_API_KEY") == "" {
		cfg.SteamAPIKey = fileCfg.SteamAPIKey
	}
	if fileCfg.SteamIDInput != "" && os.Getenv("STEAM_ID64") == "" {
		cfg.SteamIDInput = fileCfg.SteamIDInput
	}
	if fileCfg.DatabasePath != "" && os.Getenv("DATABASE_PATH") == "" && os.Getenv("DUCKDB_PATH") == "" {
		cfg.DatabasePath = resolveConfigRelativePath(path, fileCfg.DatabasePath)
	}
	if fileCfg.CollectIntervalSeconds > 0 && os.Getenv("COLLECT_INTERVAL_SECONDS") == "" {
		cfg.CollectInterval = time.Duration(fileCfg.CollectIntervalSeconds) * time.Second
	}
	if fileCfg.CollectOnStart != nil && os.Getenv("COLLECT_ON_START") == "" {
		cfg.CollectOnStart = *fileCfg.CollectOnStart
	}

	return nil
}

func resolveConfigRelativePath(configPath, value string) string {
	if filepath.IsAbs(value) {
		return value
	}
	baseDir := filepath.Dir(configPath)
	if baseDir == "." || baseDir == "" {
		return value
	}
	return filepath.Join(baseDir, value)
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getEnvAny(keys []string, fallback string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		parsed, err := strconv.Atoi(value)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if value := os.Getenv(key); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err == nil {
			return parsed
		}
	}
	return fallback
}
