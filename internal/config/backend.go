package config

import (
	"fmt"

	"github.com/spf13/viper"
)

const (
	backendKindKey   = "backendKind"
	backendConfigKey = "backendConfig"

	BackendKindPostgres = "postgres"
)

func newBackendConfig(cfg *Config) error {
	backend := viper.GetString(backendKindKey)
	cfg.BackendKind = backend

	switch backend {
	case BackendKindPostgres:
		backendCfg, err := newPostgresConfig()
		if err != nil {
			return err
		}
		cfg.BackendPostgres = backendCfg
	default:
		return fmt.Errorf("backend_config=%s not supported: %w", backend, ErrInvalidConfigValue)
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
