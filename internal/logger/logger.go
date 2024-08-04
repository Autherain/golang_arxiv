package logger

import (
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Config holds the configuration for the logger
// Learns today that capital letter are used to explicit that it can be used outisde the package
type Config struct {
	Environment    string        // The environment (e.g., "development", "production")
	LogLevel       string        // The log level (e.g., "debug", "info", "warn", "error")
	SampleRate     int           // The initial number of entries per second to allow
	ThereafterRate int           // The number of entries per second to allow after the initial burst
	SampleTime     time.Duration // The time interval between samples
	Version        float64
}

// NewLogger creates a new Zap logger with the given configuration
func NewLogger(cfg Config) (*zap.Logger, error) {
	// Define log level
	level, err := zap.ParseAtomicLevel(cfg.LogLevel)
	if err != nil {
		return nil, err
	}

	// Configure sampling
	samplerOpts := zap.WrapCore(func(core zapcore.Core) zapcore.Core {
		return zapcore.NewSamplerWithOptions(
			core,
			cfg.SampleTime,
			cfg.SampleRate,
			cfg.ThereafterRate,
		)
	})

	// Configure encoder
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	// Create base configuration
	config := zap.Config{
		Level:             level,
		Encoding:          "json",
		EncoderConfig:     encoderConfig,
		OutputPaths:       []string{"stdout"},
		ErrorOutputPaths:  []string{"stderr"},
		DisableCaller:     false,
		DisableStacktrace: false,
	}

	// Adjust configuration based on environment
	if cfg.Environment == "development" {
		config.Development = true
		config.EncoderConfig.EncodeLevel = zapcore.LowercaseColorLevelEncoder
		config.Encoding = "console"
	}

	// Build the logger
	baseLogger, err := config.Build(samplerOpts)
	if err != nil {
		return nil, err
	}

	// Add some global fields
	logger := baseLogger.With(
		zap.String("environment", cfg.Environment),
		zap.Float64("app_version", cfg.Version),
	)

	return logger, nil
}
