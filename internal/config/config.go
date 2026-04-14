package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type AuthConfig struct {
	Enable   bool   `json:"enable"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type Config struct {
	ConfigPath      string `json:"-"`
	ListenAddr      string
	SteamAPIKey     string
	SteamIDInput    string
	DatabasePath    string
	CollectInterval time.Duration
	CollectOnStart  bool
	Auth            AuthConfig
}

type fileConfig struct {
	ListenAddr             string     `json:"listen_addr"`
	SteamAPIKey            string     `json:"steam_api_key"`
	SteamIDInput           string     `json:"steam_id"`
	DatabasePath           string     `json:"database_path"`
	CollectIntervalSeconds int        `json:"collect_interval_seconds"`
	CollectOnStart         *bool      `json:"collect_on_start"`
	Auth                   AuthConfig `json:"auth"`
}

type EditableConfig = fileConfig

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
		Auth: AuthConfig{
			Enable:   getEnvBool("AUTH_ENABLE", false),
			Username: os.Getenv("AUTH_USERNAME"),
			Password: os.Getenv("AUTH_PASSWORD"),
		},
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
	if cfg.Auth.Enable && ((cfg.Auth.Username == "") != (cfg.Auth.Password == "") || cfg.Auth.Username == "") {
		return Config{}, fmt.Errorf("auth.enable is true, but auth.username and auth.password are not both set")
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
	if os.Getenv("AUTH_ENABLE") == "" {
		cfg.Auth.Enable = fileCfg.Auth.Enable
	}
	if fileCfg.Auth.Username != "" && os.Getenv("AUTH_USERNAME") == "" {
		cfg.Auth.Username = fileCfg.Auth.Username
	}
	if fileCfg.Auth.Password != "" && os.Getenv("AUTH_PASSWORD") == "" {
		cfg.Auth.Password = fileCfg.Auth.Password
	}

	return nil
}

func (cfg Config) Editable() EditableConfig {
	return EditableConfig{
		ListenAddr:             cfg.ListenAddr,
		SteamAPIKey:            cfg.SteamAPIKey,
		SteamIDInput:           cfg.SteamIDInput,
		DatabasePath:           cfg.DatabasePath,
		CollectIntervalSeconds: int(cfg.CollectInterval / time.Second),
		CollectOnStart:         boolPtr(cfg.CollectOnStart),
		Auth:                   cfg.Auth,
	}
}

func (cfg Config) EnvironmentOverrides() map[string]string {
	overrides := map[string]string{}

	addOverride := func(keys ...string) {
		for _, key := range keys {
			if value := os.Getenv(key); value != "" {
				overrides[key] = value
			}
		}
	}

	addOverride("APP_ADDR")
	addOverride("STEAM_API_KEY")
	addOverride("STEAM_ID64")
	addOverride("DATABASE_PATH", "DUCKDB_PATH")
	addOverride("COLLECT_INTERVAL_SECONDS")
	addOverride("COLLECT_ON_START")
	addOverride("AUTH_ENABLE")
	addOverride("AUTH_USERNAME")
	addOverride("AUTH_PASSWORD")

	return overrides
}

func ValidateEditable(path string, editable EditableConfig) error {
	if strings.TrimSpace(editable.ListenAddr) == "" {
		return fmt.Errorf("listen_addr is required")
	}
	if strings.TrimSpace(editable.SteamAPIKey) == "" {
		return fmt.Errorf("steam_api_key is required")
	}
	if strings.TrimSpace(editable.SteamIDInput) == "" {
		return fmt.Errorf("steam_id is required")
	}
	if strings.TrimSpace(editable.DatabasePath) == "" {
		return fmt.Errorf("database_path is required")
	}
	if editable.CollectIntervalSeconds <= 0 {
		return fmt.Errorf("collect_interval_seconds must be greater than 0")
	}
	if editable.Auth.Enable {
		username := strings.TrimSpace(editable.Auth.Username)
		password := strings.TrimSpace(editable.Auth.Password)
		if username == "" || password == "" {
			return fmt.Errorf("auth.username and auth.password are required when auth.enable is true")
		}
	}

	// Resolve the database path to make sure relative-path settings remain valid.
	_ = resolveConfigRelativePath(path, editable.DatabasePath)

	return nil
}

func SaveEditable(path string, editable EditableConfig) error {
	if err := ValidateEditable(path, editable); err != nil {
		return err
	}

	content, err := json.MarshalIndent(editable, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	content = append(content, '\n')

	if err := os.WriteFile(path, content, 0o600); err != nil {
		return fmt.Errorf("write config file %s: %w", path, err)
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

func boolPtr(value bool) *bool {
	return &value
}
