// Package config provides application configuration loaded from environment variables.
// It supports all infrastructure components used by Nkore Bank including database,
// cache, message broker, authentication, encryption, and rate limiting settings.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all application configuration values.
type Config struct {
	// Server
	ServerPort string

	// PostgreSQL
	DatabaseURL string

	// Redis
	RedisURL string

	// Kafka
	KafkaBrokers []string

	// JWT authentication
	JWTSecret string
	JWTIssuer string

	// HashiCorp Vault
	VaultAddr string

	// AES-256 encryption key for PII (hex-encoded, 64 hex chars = 32 bytes)
	AESEncryptionKey string

	// Rate limiting
	RateLimitRequests int
	RateLimitWindowSec int
}

// Load reads configuration from environment variables and returns a Config
// with sensible defaults for values that are not set.
func Load() *Config {
	return &Config{
		ServerPort:         envOrDefault("SERVER_PORT", "8080"),
		DatabaseURL:        envOrDefault("DATABASE_URL", "postgres://nkorebank:nkorebank@localhost:5432/nkorebank?sslmode=disable"),
		RedisURL:           envOrDefault("REDIS_URL", "redis://localhost:6379/0"),
		KafkaBrokers:       strings.Split(envOrDefault("KAFKA_BROKERS", "localhost:9092"), ","),
		JWTSecret:          os.Getenv("JWT_SECRET"),
		JWTIssuer:          envOrDefault("JWT_ISSUER", "nkore-bank"),
		VaultAddr:          envOrDefault("VAULT_ADDR", "http://localhost:8200"),
		AESEncryptionKey:   os.Getenv("AES_ENCRYPTION_KEY"),
		RateLimitRequests:  envOrDefaultInt("RATE_LIMIT_REQUESTS", 100),
		RateLimitWindowSec: envOrDefaultInt("RATE_LIMIT_WINDOW_SEC", 60),
	}
}

// MustLoad loads configuration and panics if critical values are missing.
// Critical values: JWT_SECRET, AES_ENCRYPTION_KEY, DATABASE_URL.
func MustLoad() *Config {
	cfg := Load()

	var missing []string
	if cfg.JWTSecret == "" {
		missing = append(missing, "JWT_SECRET")
	}
	if cfg.AESEncryptionKey == "" {
		missing = append(missing, "AES_ENCRYPTION_KEY")
	}
	if os.Getenv("DATABASE_URL") == "" {
		missing = append(missing, "DATABASE_URL")
	}

	if len(missing) > 0 {
		panic(fmt.Sprintf("config: missing critical environment variables: %s", strings.Join(missing, ", ")))
	}

	return cfg
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envOrDefaultInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
