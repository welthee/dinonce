package config

import (
	"errors"

	"github.com/spf13/viper"
)

type Config struct {
	BackendKind     string
	BackendPostgres *PostgreSQLBackendConfig

	Logger *LoggerConfig
}

var ErrInvalidConfigValue = errors.New("configuration value is invalid")

func NewConfigFromFile() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("/opt/dinonce/config")
	viper.AddConfigPath("$HOME/.config/dinonce")
	viper.AddConfigPath(".config")

	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}

	cfg := &Config{}

	loggerCfg, err := newLoggerConfig()
	if err != nil {
		return nil, err
	}
	cfg.Logger = loggerCfg

	if err := newBackendConfig(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}
