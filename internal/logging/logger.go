package logging

import (
	"fmt"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Config struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

func NewLogger(cfg Config, role, nodeName string) (*zap.Logger, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}

	var zapCfg zap.Config
	switch strings.ToLower(cfg.Format) {
	case "console":
		zapCfg = zap.NewDevelopmentConfig()
	case "json", "":
		zapCfg = zap.NewProductionConfig()
	default:
		return nil, fmt.Errorf("unsupported log format: %q (use \"json\" or \"console\")", cfg.Format)
	}

	zapCfg.Level = zap.NewAtomicLevelAt(level)
	zapCfg.EncoderConfig.TimeKey = "ts"
	zapCfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	logger, err := zapCfg.Build()
	if err != nil {
		return nil, fmt.Errorf("building logger: %w", err)
	}

	logger = logger.With(
		zap.String("role", role),
		zap.String("node_name", nodeName),
	)

	return logger, nil
}

func parseLevel(s string) (zapcore.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return zapcore.DebugLevel, nil
	case "info", "":
		return zapcore.InfoLevel, nil
	case "warn":
		return zapcore.WarnLevel, nil
	case "error":
		return zapcore.ErrorLevel, nil
	default:
		return zapcore.InfoLevel, fmt.Errorf("unsupported log level: %q", s)
	}
}
