package main

import (
	"autherain/golang_arxiv/internal/data"
	"autherain/golang_arxiv/internal/mailer"
	"autherain/golang_arxiv/internal/observability"
	"autherain/golang_arxiv/internal/vcs"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sync"

	_ "net/http/pprof"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

var version = vcs.Version()

type application struct {
	config    config
	logger    *slog.Logger
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

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

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

	telemetry, err := observability.InitTelemetry(cfg.serviceName, cfg.telemetry.tracingEndpoint, cfg.telemetry.metricEndpoint, cfg.telemetry.isInsecure, cfg.telemetry.traceRatio, cfg.telemetry.enabled)
	if err != nil {
		logger.Error("Failed to initialize telemetry", "error", err)
		os.Exit(1)
	}
	defer telemetry()

	err = app.serve()
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}
