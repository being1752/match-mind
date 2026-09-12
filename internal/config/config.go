package config

import (
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	HTTPAddr           string
	DatabaseURL        string
	AIServiceURL       string
	AIRequestTimeout   time.Duration
	WorkerPollInterval time.Duration
	FrontendOrigin     string
	JWTSecret          string
	JWTDuration        time.Duration
	EmbeddingEnabled   bool
	TestUsername       string
	TestPassword       string
	TestDisplayName    string
}

func Load() (Config, error) {
	// Read .env in local development. Existing process environment variables
	// take precedence and are never overwritten by godotenv.Load.
	_ = godotenv.Load()

	cfg := Config{
		HTTPAddr:         env("HTTP_ADDR", ":8080"),
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		AIServiceURL:     env("AI_SERVICE_URL", "http://127.0.0.1:8090"),
		FrontendOrigin:   env("FRONTEND_ORIGIN", "*"),
		JWTSecret:        env("JWT_SECRET", "match-mind-local-change-me"),
		EmbeddingEnabled: env("EMBEDDING_ENABLED", "false") == "true",
		TestUsername:     os.Getenv("TEST_ACCOUNT_USERNAME"),
		TestPassword:     os.Getenv("TEST_ACCOUNT_PASSWORD"),
		TestDisplayName:  env("TEST_ACCOUNT_DISPLAY_NAME", "测试账号"),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	var err error
	cfg.AIRequestTimeout, err = time.ParseDuration(env("AI_REQUEST_TIMEOUT", "240s"))
	if err != nil {
		return Config{}, fmt.Errorf("AI_REQUEST_TIMEOUT: %w", err)
	}
	cfg.WorkerPollInterval, err = time.ParseDuration(env("WORKER_POLL_INTERVAL", "2s"))
	if err != nil {
		return Config{}, fmt.Errorf("WORKER_POLL_INTERVAL: %w", err)
	}
	cfg.JWTDuration, err = time.ParseDuration(env("JWT_DURATION", "168h"))
	if err != nil {
		return Config{}, fmt.Errorf("JWT_DURATION: %w", err)
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
