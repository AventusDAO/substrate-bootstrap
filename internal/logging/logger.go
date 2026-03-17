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

// Supported formats: "json" (structured), "console" (dev-style), "node" (substrate-node style).
const (
	FormatJSON    = "json"
	FormatConsole = "console"
	FormatNode    = "node"
)

func NewLogger(cfg Config, nodeName string) (*zap.Logger, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}

	var zapCfg zap.Config
	switch strings.ToLower(cfg.Format) {
	case FormatConsole:
		zapCfg = zap.NewDevelopmentConfig()
	case FormatNode:
		zapCfg = nodeFormatConfig()
	case FormatJSON, "":
		zapCfg = zap.NewProductionConfig()
	default:
		return nil, fmt.Errorf("unsupported log format: %q (use %q, %q, or %q)", cfg.Format, FormatJSON, FormatConsole, FormatNode)
	}

	zapCfg.Level = zap.NewAtomicLevelAt(level)
	zapCfg.EncoderConfig.TimeKey = "ts"
	zapCfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	if strings.ToLower(cfg.Format) == FormatNode {
		zapCfg.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05")
		zapCfg.EncoderConfig.EncodeLevel = nodeLevelEncoderWithComponent
	}

	logger, err := zapCfg.Build()
	if err != nil {
		return nil, fmt.Errorf("building logger: %w", err)
	}

	logger = logger.With(zap.String("node_name", nodeName))

	return logger, nil
}

func nodeFormatConfig() zap.Config {
	cfg := zap.NewProductionConfig()
	cfg.Encoding = "console"
	cfg.DisableCaller = true
	cfg.EncoderConfig = zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    nodeLevelEncoderWithComponent,
		EncodeTime:     zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05"),
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
	return cfg
}

func nodeLevelEncoderWithComponent(l zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString("[bootstrap] [" + l.String() + "]")
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
