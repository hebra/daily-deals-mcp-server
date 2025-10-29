package internal

import (
	"os"
	"testing"
	"time"
)

func TestLoadConfig_Defaults(t *testing.T) {
	// Clear environment variables to test defaults
		envVars := []string{
			"REQUESTY_API_KEY", "FETCH_HOUR", "CACHE_FILE", "SPECIALS_URL",
			"BUSINESS_NAME", "PORT", "TIMEZONE", "HTTP_TIMEOUT", "REQUESTY_TIMEOUT", "OVERALL_TIMEOUT",
			"LOCATION_LATITUDE", "LOCATION_LONGITUDE", "LOCATION_ADDRESS",
			"LOCATION_CITY", "LOCATION_STATE", "LOCATION_ZIP", "LOCATION_COUNTRY",
		}

	// Save original values
	originalValues := make(map[string]string)
	for _, envVar := range envVars {
		originalValues[envVar] = os.Getenv(envVar)
		os.Unsetenv(envVar)
	}
	defer func() {
		// Restore original values
		for envVar, value := range originalValues {
			if value != "" {
				os.Setenv(envVar, value)
			} else {
				os.Unsetenv(envVar)
			}
		}
	}()

	// This should fail because REQUESTY_API_KEY is required
	// We can't test this directly because LoadConfig calls os.Exit(1)
	// Instead, we'll test the validation separately
	config := &Config{}
	err := config.Validate()
	if err == nil {
		t.Error("Expected validation to fail with missing REQUESTY_API_KEY")
	}
	if validationErr, ok := err.(*ValidationError); ok {
		if validationErr.Field != "REQUESTY_API_KEY" {
			t.Errorf("Expected error field to be 'REQUESTY_API_KEY', got %s", validationErr.Field)
		}
	} else {
		t.Errorf("Expected ValidationError, got %T", err)
	}
}

func TestLoadConfig_WithAPIKey(t *testing.T) {
	// Clear environment variables
		envVars := []string{
			"REQUESTY_API_KEY", "FETCH_HOUR", "CACHE_FILE", "SPECIALS_URL",
			"BUSINESS_NAME", "PORT", "TIMEZONE", "HTTP_TIMEOUT", "REQUESTY_TIMEOUT", "OVERALL_TIMEOUT",
			"LOCATION_LATITUDE", "LOCATION_LONGITUDE", "LOCATION_ADDRESS",
			"LOCATION_CITY", "LOCATION_STATE", "LOCATION_ZIP", "LOCATION_COUNTRY",
		}

	// Save original values
	originalValues := make(map[string]string)
	for _, envVar := range envVars {
		originalValues[envVar] = os.Getenv(envVar)
		os.Unsetenv(envVar)
	}
	defer func() {
		// Restore original values
		for envVar, value := range originalValues {
			if value != "" {
				os.Setenv(envVar, value)
			} else {
				os.Unsetenv(envVar)
			}
		}
	}()

	// Set required API key
	os.Setenv("REQUESTY_API_KEY", "test-api-key")

	config := LoadConfig()

	// Test defaults
	if config.FetchHour != 7 {
		t.Errorf("Expected FetchHour to be 7, got %d", config.FetchHour)
	}
	if config.CacheFile != "bigwatermelon-dailydeals.cached.json" {
		t.Errorf("Expected CacheFile to be 'bigwatermelon-dailydeals.cached.json', got %s", config.CacheFile)
	}
	if config.Port != "8080" {
		t.Errorf("Expected Port to be '8080', got %s", config.Port)
	}
	if config.RequestyModel != "google/gemini-2.5-flash" {
		t.Errorf("Expected RequestyModel to be 'google/gemini-2.5-flash', got %s", config.RequestyModel)
	}
	if config.HTTPTimeout != 30*time.Second {
		t.Errorf("Expected HTTPTimeout to be 30s, got %v", config.HTTPTimeout)
	}
}

func TestLoadConfig_CustomValues(t *testing.T) {
	// Clear environment variables
		envVars := []string{
			"REQUESTY_API_KEY", "FETCH_HOUR", "CACHE_FILE", "SPECIALS_URL",
			"BUSINESS_NAME", "PORT", "TIMEZONE", "HTTP_TIMEOUT", "REQUESTY_TIMEOUT", "OVERALL_TIMEOUT",
			"LOCATION_LATITUDE", "LOCATION_LONGITUDE", "LOCATION_ADDRESS",
			"LOCATION_CITY", "LOCATION_STATE", "LOCATION_ZIP", "LOCATION_COUNTRY",
		}

	// Save original values
	originalValues := make(map[string]string)
	for _, envVar := range envVars {
		originalValues[envVar] = os.Getenv(envVar)
		os.Unsetenv(envVar)
	}
	defer func() {
		// Restore original values
		for envVar, value := range originalValues {
			if value != "" {
				os.Setenv(envVar, value)
			} else {
				os.Unsetenv(envVar)
			}
		}
	}()

	// Set custom values
	os.Setenv("REQUESTY_API_KEY", "custom-api-key")
	os.Setenv("FETCH_HOUR", "9")
	os.Setenv("CACHE_FILE", "custom-cache.json")
	os.Setenv("PORT", "3000")
	os.Setenv("HTTP_TIMEOUT", "45s")
	os.Setenv("LOCATION_LATITUDE", "-33.8688")

	config := LoadConfig()

	if config.RequestyAPIKey != "custom-api-key" {
		t.Errorf("Expected RequestyAPIKey to be 'custom-api-key', got %s", config.RequestyAPIKey)
	}
	if config.FetchHour != 9 {
		t.Errorf("Expected FetchHour to be 9, got %d", config.FetchHour)
	}
	if config.CacheFile != "custom-cache.json" {
		t.Errorf("Expected CacheFile to be 'custom-cache.json', got %s", config.CacheFile)
	}
	if config.Port != "3000" {
		t.Errorf("Expected Port to be '3000', got %s", config.Port)
	}
	if config.HTTPTimeout != 45*time.Second {
		t.Errorf("Expected HTTPTimeout to be 45s, got %v", config.HTTPTimeout)
	}
	if config.Location.Latitude != -33.8688 {
		t.Errorf("Expected Location.Latitude to be -33.8688, got %f", config.Location.Latitude)
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name      string
		config    *Config
		shouldErr bool
		errField  string
	}{
		{
			name: "valid config",
			config: &Config{
				RequestyAPIKey:          "test-key",
				RequestyBaseURL:         "https://router.requesty.ai/v1",
				RequestyModel:           "google/gemini-2.5-flash",
				FetchHour:               7,
				CacheFile:               "test.json",
				SpecialsURL:             "http://example.com",
				Port:                    "8080",
				Timezone:                "UTC",
				MaxRetries:              3,
				RetryBaseDelay:          time.Second,
				RateLimitRequests:       10,
				RateLimitWindow:         time.Minute,
				MaxConcurrentGoroutines: 5,
			},
			shouldErr: false,
		},
		{
			name: "missing API key",
			config: &Config{
				FetchHour:   7,
				CacheFile:   "test.json",
				SpecialsURL: "http://example.com",
				Port:        "8080",
				Timezone:    "UTC",
			},
			shouldErr: true,
			errField:   "REQUESTY_API_KEY",
		},
		{
			name: "invalid fetch hour - negative",
			config: &Config{
				RequestyAPIKey:  "test-key",
				RequestyBaseURL: "https://router.requesty.ai/v1",
				RequestyModel:   "google/gemini-2.5-flash",
				FetchHour:       -1,
				CacheFile:       "test.json",
				SpecialsURL:     "http://example.com",
				Port:            "8080",
				Timezone:        "UTC",
			},
			shouldErr: true,
			errField:   "FETCH_HOUR",
		},
		{
			name: "invalid fetch hour - too high",
			config: &Config{
				RequestyAPIKey:  "test-key",
				RequestyBaseURL: "https://router.requesty.ai/v1",
				RequestyModel:   "google/gemini-2.5-flash",
				FetchHour:       25,
				CacheFile:       "test.json",
				SpecialsURL:     "http://example.com",
				Port:            "8080",
				Timezone:        "UTC",
			},
			shouldErr: true,
			errField:   "FETCH_HOUR",
		},
		{
			name: "missing cache file",
			config: &Config{
				RequestyAPIKey:  "test-key",
				RequestyBaseURL: "https://router.requesty.ai/v1",
				RequestyModel:   "google/gemini-2.5-flash",
				FetchHour:       7,
				SpecialsURL:     "http://example.com",
				Port:            "8080",
				Timezone:        "UTC",
			},
			shouldErr: true,
			errField:   "CACHE_FILE",
		},
		{
			name: "missing specials URL",
			config: &Config{
				RequestyAPIKey:  "test-key",
				RequestyBaseURL: "https://router.requesty.ai/v1",
				RequestyModel:   "google/gemini-2.5-flash",
				FetchHour:       7,
				CacheFile:       "test.json",
				Port:            "8080",
				Timezone:        "UTC",
			},
			shouldErr: true,
			errField:   "SPECIALS_URL",
		},
		{
			name: "missing port",
			config: &Config{
				RequestyAPIKey:  "test-key",
				RequestyBaseURL: "https://router.requesty.ai/v1",
				RequestyModel:   "google/gemini-2.5-flash",
				FetchHour:       7,
				CacheFile:       "test.json",
				SpecialsURL:     "http://example.com",
				Timezone:        "UTC",
			},
			shouldErr: true,
			errField:   "PORT",
		},
		{
			name: "invalid timezone",
			config: &Config{
				RequestyAPIKey:  "test-key",
				RequestyBaseURL: "https://router.requesty.ai/v1",
				RequestyModel:   "google/gemini-2.5-flash",
				FetchHour:       7,
				CacheFile:       "test.json",
				SpecialsURL:     "http://example.com",
				Port:            "8080",
				Timezone:        "Invalid/Timezone",
			},
			shouldErr: true,
			errField:   "TIMEZONE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.shouldErr {
				if err == nil {
					t.Error("Expected validation to fail, but it passed")
					return
				}
				if validationErr, ok := err.(*ValidationError); ok {
					if validationErr.Field != tt.errField {
						t.Errorf("Expected error field to be %s, got %s", tt.errField, validationErr.Field)
					}
				} else {
					t.Errorf("Expected ValidationError, got %T", err)
				}
			} else {
				if err != nil {
					t.Errorf("Expected validation to pass, but got error: %v", err)
				}
			}
		})
	}
}

func TestGetEnvWithDefault(t *testing.T) {
	// Test with existing env var
	os.Setenv("TEST_VAR", "existing_value")
	defer os.Unsetenv("TEST_VAR")

	result := getEnvWithDefault("TEST_VAR", "default_value")
	if result != "existing_value" {
		t.Errorf("Expected 'existing_value', got '%s'", result)
	}

	// Test with non-existing env var
	result = getEnvWithDefault("NON_EXISTING_VAR", "default_value")
	if result != "default_value" {
		t.Errorf("Expected 'default_value', got '%s'", result)
	}
}

func TestGetEnvAsInt(t *testing.T) {
	// Test with valid integer
	os.Setenv("TEST_INT", "42")
	defer os.Unsetenv("TEST_INT")

	result := getEnvAsInt("TEST_INT", 10)
	if result != 42 {
		t.Errorf("Expected 42, got %d", result)
	}

	// Test with invalid integer (should return default)
	os.Setenv("TEST_INT", "not_a_number")
	result = getEnvAsInt("TEST_INT", 10)
	if result != 10 {
		t.Errorf("Expected default 10, got %d", result)
	}

	// Test with non-existing env var
	result = getEnvAsInt("NON_EXISTING_INT", 99)
	if result != 99 {
		t.Errorf("Expected default 99, got %d", result)
	}
}

func TestGetEnvAsFloat(t *testing.T) {
	// Test with valid float
	os.Setenv("TEST_FLOAT", "3.14")
	defer os.Unsetenv("TEST_FLOAT")

	result := getEnvAsFloat("TEST_FLOAT", 1.0)
	if result != 3.14 {
		t.Errorf("Expected 3.14, got %f", result)
	}

	// Test with invalid float (should return default)
	os.Setenv("TEST_FLOAT", "not_a_number")
	result = getEnvAsFloat("TEST_FLOAT", 2.5)
	if result != 2.5 {
		t.Errorf("Expected default 2.5, got %f", result)
	}

	// Test with non-existing env var
	result = getEnvAsFloat("NON_EXISTING_FLOAT", 9.99)
	if result != 9.99 {
		t.Errorf("Expected default 9.99, got %f", result)
	}
}

func TestGetEnvAsDuration(t *testing.T) {
	// Test with valid duration
	os.Setenv("TEST_DURATION", "2m30s")
	defer os.Unsetenv("TEST_DURATION")

	result := getEnvAsDuration("TEST_DURATION", time.Minute)
	expected := 2*time.Minute + 30*time.Second
	if result != expected {
		t.Errorf("Expected %v, got %v", expected, result)
	}

	// Test with invalid duration (should return default)
	os.Setenv("TEST_DURATION", "not_a_duration")
	result = getEnvAsDuration("TEST_DURATION", time.Hour)
	if result != time.Hour {
		t.Errorf("Expected default 1h, got %v", result)
	}

	// Test with non-existing env var
	result = getEnvAsDuration("NON_EXISTING_DURATION", 5*time.Minute)
	if result != 5*time.Minute {
		t.Errorf("Expected default 5m, got %v", result)
	}
}
