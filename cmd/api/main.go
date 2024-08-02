package main

import (
	"autherain/golang_arxiv/internal/data"
	"autherain/golang_arxiv/internal/mailer"
	"autherain/golang_arxiv/internal/vcs"
	"context"
	"database/sql"
	"expvar"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

var version = vcs.Version()

type config struct {
	port int
	env  string
	db   struct {
		dsn          string
		maxOpenConns int
		maxIdleConns int
		maxIdleTime  time.Duration
	}
	limiter struct {
		enabled bool
		rps     float64
		burst   int
	}
	smtp struct {
		host     string
		port     int
		username string
		password string
		sender   string
	}
	cors struct {
		trustedOrigins []string
	}
}

type application struct {
	config config
	logger *slog.Logger
	models data.Models
	mailer mailer.Mailer
	wg     sync.WaitGroup
}

func main() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		fmt.Printf("Error loading .env file: %v\n", err)
	}

	var cfg config

	// Load configuration from environment variables
	cfg.port = getEnvAsInt("PORT", 4000)
	cfg.env = os.Getenv("ENV")

	// Generate DSN string
	dbUsername := os.Getenv("DB_USERNAME")
	dbPassword := os.Getenv("DB_PASSWORD")
	dbHost := getDBHost()
	dbPort := os.Getenv("DB_PORT")
	dbName := os.Getenv("DB_DATABASE")
	cfg.db.dsn = fmt.Sprintf("postgresql://%s:%s@%s:%s/%s?sslmode=disable", dbUsername, dbPassword, dbHost, dbPort, dbName)

	cfg.db.maxOpenConns = getEnvAsInt("DB_MAX_OPEN_CONNS", 25)
	cfg.db.maxIdleConns = getEnvAsInt("DB_MAX_IDLE_CONNS", 25)
	cfg.db.maxIdleTime = getEnvAsDuration("DB_MAX_IDLE_TIME", 15*time.Minute)
	cfg.limiter.enabled = getEnvAsBool("LIMITER_ENABLED", true)
	cfg.limiter.rps = getEnvAsFloat64("LIMITER_RPS", 2)
	cfg.limiter.burst = getEnvAsInt("LIMITER_BURST", 4)
	cfg.smtp.host = os.Getenv("SMTP_HOST")
	cfg.smtp.port = getEnvAsInt("SMTP_PORT", 25)
	cfg.smtp.username = os.Getenv("SMTP_USERNAME")
	cfg.smtp.password = os.Getenv("SMTP_PASSWORD")
	cfg.smtp.sender = os.Getenv("SMTP_SENDER")
	cfg.cors.trustedOrigins = strings.Fields(os.Getenv("CORS_TRUSTED_ORIGINS"))

	// Flag for version display
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

	expvar.NewString("version").Set(version)
	expvar.Publish("goroutines", expvar.Func(func() any {
		return runtime.NumGoroutine()
	}))
	expvar.Publish("database", expvar.Func(func() any {
		return db.Stats()
	}))
	expvar.Publish("timestamp", expvar.Func(func() any {
		return time.Now().Unix()
	}))

	app := &application{
		config: cfg,
		logger: logger,
		models: data.NewModels(db),
		mailer: mailer.New(cfg.smtp.host, cfg.smtp.port, cfg.smtp.username, cfg.smtp.password, cfg.smtp.sender),
	}

	err = app.serve()
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}

func getDBHost() string {
	// Try to resolve the 'psql' hostname
	_, err := net.LookupHost("psql")
	if err == nil {
		// If 'psql' resolves, we're in a Docker environment
		return "psql"
	}
	// Otherwise, we're likely running locally
	return "localhost"
}

func openDB(cfg config) (*sql.DB, error) {
	db, err := sql.Open("postgres", cfg.db.dsn)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(cfg.db.maxOpenConns)
	db.SetMaxIdleConns(cfg.db.maxIdleConns)
	db.SetConnMaxIdleTime(cfg.db.maxIdleTime)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = db.PingContext(ctx)
	if err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

// Helper functions to get environment variables
func getEnvAsInt(key string, defaultVal int) int {
	valueStr := os.Getenv(key)
	if value, err := strconv.Atoi(valueStr); err == nil {
		return value
	}
	return defaultVal
}

func getEnvAsFloat64(key string, defaultVal float64) float64 {
	valueStr := os.Getenv(key)
	if value, err := strconv.ParseFloat(valueStr, 64); err == nil {
		return value
	}
	return defaultVal
}

func getEnvAsBool(key string, defaultVal bool) bool {
	valueStr := os.Getenv(key)
	if value, err := strconv.ParseBool(valueStr); err == nil {
		return value
	}
	return defaultVal
}

func getEnvAsDuration(key string, defaultVal time.Duration) time.Duration {
	valueStr := os.Getenv(key)
	if value, err := time.ParseDuration(valueStr); err == nil {
		return value
	}
	return defaultVal
}
