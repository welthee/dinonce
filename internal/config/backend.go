package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

const (
	backendKindKey   = "backendKind"
	backendConfigKey = "backendConfig"

	BackendKindPostgres = "postgres"
)

func newBackendConfig(cfg *Config) error {
	kind := viper.GetString(backendKindKey)
	if strings.TrimSpace(kind) == "" {
		return fmt.Errorf("missing config %s: %w", backendConfigKey, ErrInvalidConfigValue)
	}

	cfg.BackendKind = kind

	switch kind {
	case BackendKindPostgres:
		backendCfg, err := newPostgresConfig()
		if err != nil {
			return err
		}
		cfg.BackendPostgres = backendCfg
	default:
		return fmt.Errorf("%s=%s not supported: %w", backendConfigKey, kind, ErrInvalidConfigValue)
	}

	return nil
}

type PostgreSQLBackendConfig struct {
	Host          string
	Port          int
	User          string
	Password      string
	DatabaseName  string
	MigrationsDir string
}

func newPostgresConfig() (*PostgreSQLBackendConfig, error) {
	var cfg PostgreSQLBackendConfig
	if err := viper.UnmarshalKey(backendConfigKey, &cfg); err != nil {
		return nil, fmt.Errorf("backend_kind=%s: %w", backendKindKey, ErrInvalidConfigValue)
	}

	if cfg.MigrationsDir == "" {
		const defaultDir = "file://./scripts/psql/migrations"
		cfg.MigrationsDir = defaultDir
	}

	return &cfg, nil
}
