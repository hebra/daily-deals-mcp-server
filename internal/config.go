package internal

import (
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"
)

// Config holds all configuration for the application
type Config struct {
	RequestyAPIKey          string
	RequestyBaseURL         string
	RequestyModel           string
	RequestyMaxTokens       int
	RequestyTemperature     float64
	FetchHour               int
	CacheFile               string
	SpecialsURL             string
	BusinessName            string
	Port                    string
	Timezone                string
	Location                Location
	HTTPTimeout             time.Duration
	RequestyTimeout         time.Duration
	OverallTimeout          time.Duration
	MaxRetries              int
	RetryBaseDelay          time.Duration
	HTTPClient              *http.Client
	RateLimitRequests       int
	RateLimitWindow         time.Duration
	MaxConcurrentGoroutines int
}

// LoadConfig loads configuration from environment variables with defaults
func LoadConfig() *Config {
	httpTimeout := getEnvAsDuration("HTTP_TIMEOUT", 30*time.Second)

	config := &Config{
		RequestyAPIKey:          getEnvWithDefault("REQUESTY_API_KEY", ""),
		RequestyBaseURL:         getEnvWithDefault("REQUESTY_BASE_URL", "https://router.requesty.ai/v1"),
		RequestyModel:           getEnvWithDefault("REQUESTY_MODEL", "google/gemini-2.5-flash"),
		RequestyMaxTokens:       getEnvAsInt("REQUESTY_MAX_TOKENS", 4096),
		RequestyTemperature:     getEnvAsFloat("REQUESTY_TEMPERATURE", 0.0),
		FetchHour:               getEnvAsInt("FETCH_HOUR", 7),
		CacheFile:               getEnvWithDefault("CACHE_FILE", "bigwatermelon-dailydeals.cached.json"),
		SpecialsURL:             getEnvWithDefault("SPECIALS_URL", "https://www.bigwatermelon.com.au/category/specials/"),
		BusinessName:            getEnvWithDefault("BUSINESS_NAME", "Big Watermelon Bushy Park"),
		Port:                    getEnvWithDefault("PORT", "8080"),
		Timezone:                getEnvWithDefault("TIMEZONE", "Australia/Melbourne"),
		HTTPTimeout:             httpTimeout,
		RequestyTimeout:         getEnvAsDuration("REQUESTY_TIMEOUT", 60*time.Second),
		OverallTimeout:          getEnvAsDuration("OVERALL_TIMEOUT", 300*time.Second),
		MaxRetries:              getEnvAsInt("MAX_RETRIES", 3),
		RetryBaseDelay:          getEnvAsDuration("RETRY_BASE_DELAY", 1*time.Second),
		RateLimitRequests:       getEnvAsInt("RATE_LIMIT_REQUESTS", 100),
		RateLimitWindow:         getEnvAsDuration("RATE_LIMIT_WINDOW", 1*time.Minute),
		MaxConcurrentGoroutines: getEnvAsInt("MAX_CONCURRENT_GOROUTINES", 5),
		HTTPClient: &http.Client{
			Timeout: httpTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		Location: Location{
			Latitude:  getEnvAsFloat("LOCATION_LATITUDE", -37.8748714),
			Longitude: getEnvAsFloat("LOCATION_LONGITUDE", 145.2053244),
			Address:   getEnvWithDefault("LOCATION_ADDRESS", "1161 High St Rd"),
			City:      getEnvWithDefault("LOCATION_CITY", "Wantirna South"),
			State:     getEnvWithDefault("LOCATION_STATE", "VIC"),
			Zip:       getEnvWithDefault("LOCATION_ZIP", "3152"),
			Country:   getEnvWithDefault("LOCATION_COUNTRY", "Australia"),
		},
	}

	// Validate required configuration
	if err := config.Validate(); err != nil {
		slog.Error("Configuration validation failed", "error", err)
		os.Exit(1)
	}

	return config
}

// Validate checks that required configuration values are present
func (c *Config) Validate() error {
	if c.RequestyAPIKey == "" {
		return &ValidationError{Field: "REQUESTY_API_KEY", Message: "API key is required"}
	}

	if c.RequestyBaseURL == "" {
		return &ValidationError{Field: "REQUESTY_BASE_URL", Message: "Requesty base URL is required"}
	}

	if c.RequestyModel == "" {
		return &ValidationError{Field: "REQUESTY_MODEL", Message: "Requesty model is required"}
	}

	if c.FetchHour < 0 || c.FetchHour > 23 {
		return &ValidationError{Field: "FETCH_HOUR", Message: "must be between 0 and 23"}
	}

	if c.CacheFile == "" {
		return &ValidationError{Field: "CACHE_FILE", Message: "cache file path is required"}
	}

	if c.SpecialsURL == "" {
		return &ValidationError{Field: "SPECIALS_URL", Message: "specials URL is required"}
	}

	if c.Port == "" {
		return &ValidationError{Field: "PORT", Message: "port is required"}
	}

	// Validate timezone
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return &ValidationError{Field: "TIMEZONE", Message: "invalid timezone: " + err.Error()}
	}

	if c.MaxRetries < 0 {
		return &ValidationError{Field: "MAX_RETRIES", Message: "must be non-negative"}
	}

	if c.RetryBaseDelay <= 0 {
		return &ValidationError{Field: "RETRY_BASE_DELAY", Message: "must be positive"}
	}

	if c.RateLimitRequests <= 0 {
		return &ValidationError{Field: "RATE_LIMIT_REQUESTS", Message: "must be positive"}
	}

	if c.RateLimitWindow <= 0 {
		return &ValidationError{Field: "RATE_LIMIT_WINDOW", Message: "must be positive"}
	}

	if c.MaxConcurrentGoroutines <= 0 {
		return &ValidationError{Field: "MAX_CONCURRENT_GOROUTINES", Message: "must be positive"}
	}

	return nil
}

// ValidationError represents a configuration validation error
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return "validation error for " + e.Field + ": " + e.Message
}

// Helper functions for environment variable parsing
func getEnvWithDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
		slog.Warn("Invalid integer value for environment variable, using default", "key", key, "value", value, "default", defaultValue)
	}
	return defaultValue
}

func getEnvAsFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if floatValue, err := strconv.ParseFloat(value, 64); err == nil {
			return floatValue
		}
		slog.Warn("Invalid float value for environment variable, using default", "key", key, "value", value, "default", defaultValue)
	}
	return defaultValue
}

func getEnvAsDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
		slog.Warn("Invalid duration value for environment variable, using default", "key", key, "value", value, "default", defaultValue)
	}
	return defaultValue
}
