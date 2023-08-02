package config

import (
	"fmt"
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

type LoggerConfig struct {
	Level zerolog.Level
	Kind  string
}

func ConfigureLogger(cfg LoggerConfig) {
	switch cfg.Kind {
	case "console":
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
		log.Info().Str("level", cfg.Level.String()).Msg("Logger initialized: Console logger selected.")
	default:
		log.Info().Str("level", cfg.Level.String()).Msg("Logger initialized: JSON logger selected.")
	}

	zerolog.SetGlobalLevel(cfg.Level)
	zerolog.DefaultContextLogger = &log.Logger
}

func newLoggerConfig() (*LoggerConfig, error) {
	logKind := viper.GetString("logger.kind")

	logLevelStr := viper.GetString("logger.level")
	if logLevelStr == "" {
		logLevelStr = zerolog.InfoLevel.String()
	}

	logLevel, err := zerolog.ParseLevel(logLevelStr)
	if err != nil {
		return nil, fmt.Errorf("log_level=%s: %w", logLevelStr, ErrInvalidConfigValue)
	}

	return &LoggerConfig{
		Kind:  logKind,
		Level: logLevel,
	}, nil
}
