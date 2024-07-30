package server

import (
	"fmt"
	"golang_arxiv/internal/database"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/httplog/v2"
	_ "github.com/joho/godotenv/autoload"
)

type Server struct {
	port   int
	logger httplog.Logger
	db     database.Service
}

func NewServer() *http.Server {
	port, _ := strconv.Atoi(os.Getenv("PORT"))

	logger := httplog.NewLogger(os.Getenv("APP_NAME"), httplog.Options{
		// JSON:             true,
		LogLevel: slog.LevelDebug,
		// Concise:          true,
		RequestHeaders:   true,
		MessageFieldName: "message",
		// TimeFieldFormat: time.RFC850,
		Tags: map[string]string{
			"version": os.Getenv("APP_VERSION"),
			"env":     os.Getenv("APP_ENV"),
		},
		QuietDownRoutes: []string{
			"/",
			"/ping",
		},
		QuietDownPeriod: 10 * time.Second,
		// SourceFieldName: "source",
	})

	NewServer := &Server{
		port:   port,
		logger: *logger,
		db:     database.New(),
	}

	// Declare Server config
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", NewServer.port),
		Handler:      NewServer.RegisterRoutes(),
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	return server
}
