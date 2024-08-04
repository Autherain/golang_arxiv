package main

import (
	"autherain/golang_arxiv/internal/data"
	"autherain/golang_arxiv/internal/logger"
	"autherain/golang_arxiv/internal/mailer"
	"autherain/golang_arxiv/internal/observability"
	"autherain/golang_arxiv/internal/vcs"
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	_ "net/http/pprof"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/uptrace/opentelemetry-go-extra/otelzap"
	"go.uber.org/zap"
)

var version = vcs.Version()

type application struct {
	config    config
	logger    *otelzap.Logger
	models    data.Models
	mailer    mailer.Mailer
	wg        sync.WaitGroup
	telemetry observability.ObservabilityShutdownFunc
}

func main() {
	if err := godotenv.Load(); err != nil {
		fmt.Printf("Error loading .env file: %v\n", err)
	}

	cfg := loadConfig()

	displayVersion := flag.Bool("version", false, "Display version and exit")
	flag.Parse()

	if *displayVersion {
		fmt.Printf("Version:\t%s\n", version)
		os.Exit(0)
	}

	logConfig := logger.Config{
		Environment:    cfg.env,
		LogLevel:       cfg.logger.logLevel,       // or get from your config
		SampleRate:     cfg.logger.sampleRate,     // Allow 100 logs per interval initially
		ThereafterRate: cfg.logger.thereAfterRate, // Allow 100 logs per interval thereafter
		SampleTime:     time.Second,               // Check every second
		Version:        cfg.version,
	}

	zapLogger, err := logger.NewLogger(logConfig)
	if err != nil {
		fmt.Printf("Failed to create logger: %v\n", err)
		os.Exit(1)
	}
	defer zapLogger.Sync()

	// Wrap Zap logger with OpenTelemetry
	// If the telemetry not enabled then it is no big deal beacause i'll still work the same way
	// Indeed, log will capture trace
	logger := otelzap.New(zapLogger)
	otelzap.ReplaceGlobals(logger)

	db, err := openDB(cfg)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	defer db.Close()
	logger.Info("database connection pool established")

	app := &application{
		config: cfg,
		logger: logger,
		models: data.NewModels(db),
		mailer: mailer.New(cfg.smtp.host, cfg.smtp.port, cfg.smtp.username, cfg.smtp.password, cfg.smtp.sender),
	}

	telemetry, err := observability.InitTelemetry(cfg.serviceName,
		cfg.telemetry.tracingEndpoint,
		cfg.telemetry.metricEndpoint,
		cfg.telemetry.isInsecure,
		cfg.telemetry.traceRatio,
		cfg.telemetry.enabled)
	if err != nil {
		logger.Error("Failed to initialize telemetry", zap.Error(err))
		os.Exit(1)
	}
	defer telemetry()

	err = app.serve()
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}
