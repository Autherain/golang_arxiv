package main

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type config struct {
	port        int
	env         string
	serviceName string
	db          struct {
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
	telemetry struct {
		tracingEndpoint string
		metricEndpoint  string
		isInsecure      bool
		traceRatio      float64
	}
}

func loadConfig() config {
	var cfg config

	cfg.port = getEnvAsInt("API_PORT", 4000)
	cfg.env = os.Getenv("ENV")
	cfg.serviceName = os.Getenv("SERVICE_NAME")

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

	cfg.telemetry.tracingEndpoint = os.Getenv("TRACE_ENDPOINT")
	cfg.telemetry.metricEndpoint = os.Getenv("METRIC_ENDPOINT")
	cfg.telemetry.isInsecure = getEnvAsBool("ISINSECURE", true)
	cfg.telemetry.traceRatio = getEnvAsFloat64("TRACE_RATIO", 0.1)

	return cfg
}

func getDBHost() string {
	_, err := net.LookupHost("psql")
	if err == nil {
		return "psql"
	}
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
