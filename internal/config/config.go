package config

import (
	"os"

	"github.com/joho/godotenv"
)

// AppConfig holds the application configuration loaded from environment variables.
var AppConfig Config

// Config represents the application configuration.
type Config struct {
	Port     string
	MongoURI string

	DomainStatic string // dev fallback for domain_static setting (env: DOMAIN_STATIC)

	// Redis is optional. Without it, cache entries are stored in .cached.
	RedisURL string
}

// Load reads configuration from environment variables (and .env file).
func Load() {
	// Load .env file if present (ignore error if not found)
	godotenv.Load()

	AppConfig = Config{
		Port:         getEnv("PORT", "8082"),
		MongoURI:     getEnv("DATABASE_URL", "mongodb://localhost:27017"),
		DomainStatic: getEnv("DOMAIN_STATIC", ""),
		RedisURL:     getEnv("REDIS_URL", getEnv("RADIS_URL", "")),
	}
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
