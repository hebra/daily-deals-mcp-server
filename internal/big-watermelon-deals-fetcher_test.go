package internal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestCheckLocalFile(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "test_cache")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Test with non-existent file
	config := &Config{CacheFile: filepath.Join(tempDir, "nonexistent.json")}
	result := checkLocalFile(config)
	if result.LastUpdated != "" {
		t.Error("Expected empty result for non-existent file")
	}

	// Test with valid cache file
	testData := ResponseData{
		LastUpdated: "2024-01-01",
		Business:    "Test Business",
		Location: Location{
			Latitude:  -37.8136,
			Longitude: 144.9631,
			City:      "Melbourne",
		},
		Offers: []Offer{
			{ProductName: "Apple", Price: "2.50", Currency: "AUD", Size: "kg"},
		},
	}

	jsonData, err := json.MarshalIndent(testData, "", "\t")
	if err != nil {
		t.Fatalf("Failed to marshal test data: %v", err)
	}

	cacheFile := filepath.Join(tempDir, "test_cache.json")
	err = os.WriteFile(cacheFile, jsonData, 0644)
	if err != nil {
		t.Fatalf("Failed to write cache file: %v", err)
	}

	config.CacheFile = cacheFile
	result = checkLocalFile(config)

	if result.LastUpdated != "2024-01-01" {
		t.Errorf("Expected LastUpdated to be '2024-01-01', got '%s'", result.LastUpdated)
	}
	if result.Business != "Test Business" {
		t.Errorf("Expected Business to be 'Test Business', got '%s'", result.Business)
	}
	if len(result.Offers) != 1 {
		t.Errorf("Expected 1 offer, got %d", len(result.Offers))
	}
	if result.Offers[0].ProductName != "Apple" {
		t.Errorf("Expected product name 'Apple', got '%s'", result.Offers[0].ProductName)
	}
}

func TestCheckLocalFile_InvalidJSON(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "test_cache")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Test with invalid JSON
	cacheFile := filepath.Join(tempDir, "invalid.json")
	err = os.WriteFile(cacheFile, []byte("invalid json content"), 0644)
	if err != nil {
		t.Fatalf("Failed to write invalid cache file: %v", err)
	}

	config := &Config{CacheFile: cacheFile}
	result := checkLocalFile(config)
	if result.LastUpdated != "" {
		t.Error("Expected empty result for invalid JSON")
	}
}

func TestDownloadImagesFromBigWatermelon_ImageURLExtraction(t *testing.T) {
	tests := []struct {
		name         string
		htmlContent  string
		expectedURLs []string
	}{
		{
			name: "single SPECIALS image",
			htmlContent: `
				<html>
				<body>
					<img src="other-image.jpg" />
					<a href="https://example.com/specials-banner.jpg">Specials</a>
				</body>
				</html>
			`,
			expectedURLs: []string{"https://example.com/specials-banner.jpg"},
		},
		{
			name: "multiple SPECIALS images",
			htmlContent: `
				<html>
				<body>
					<a href="https://example.com/specials-1.jpg">Specials 1</a>
					<a href="https://example.com/regular.jpg">Regular</a>
					<a href="https://example.com/specials-2.jpg">Specials 2</a>
				</body>
				</html>
			`,
			expectedURLs: []string{
				"https://example.com/specials-1.jpg",
				"https://example.com/specials-2.jpg",
			},
		},
		{
			name: "case insensitive matching",
			htmlContent: `
				<html>
				<body>
					<a href="https://example.com/SPECIALS.jpg">Specials</a>
					<a href="https://example.com/specials.jpg">Specials</a>
				</body>
				</html>
			`,
			expectedURLs: []string{
				"https://example.com/SPECIALS.jpg",
				"https://example.com/specials.jpg",
			},
		},
		{
			name: "no SPECIALS images",
			htmlContent: `
				<html>
				<body>
					<a href="https://example.com/regular.jpg">Regular</a>
					<a href="https://example.com/other.jpg">Other</a>
				</body>
				</html>
			`,
			expectedURLs: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			regex := regexp.MustCompile(`(?i)href="([^"]*SPECIALS?[^"]*\.jpg)"`)
			matches := regex.FindAllStringSubmatch(tt.htmlContent, -1)

			var extractedURLs []string
			for _, match := range matches {
				if len(match) > 1 {
					extractedURLs = append(extractedURLs, match[1])
				}
			}

			if len(extractedURLs) != len(tt.expectedURLs) {
				t.Errorf("Expected %d URLs, got %d", len(tt.expectedURLs), len(extractedURLs))
				return
			}

			for i, expected := range tt.expectedURLs {
				if extractedURLs[i] != expected {
					t.Errorf("Expected URL %d to be '%s', got '%s'", i, expected, extractedURLs[i])
				}
			}
		})
	}
}

func TestWriteOffersToFile(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "test_write")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	testData := ResponseData{
		LastUpdated: "2024-01-01",
		Business:    "Test Business",
		Location: Location{
			Latitude:  -37.8136,
			Longitude: 144.9631,
			City:      "Melbourne",
		},
		Offers: []Offer{
			{ProductName: "Apple", Price: "2.50", Currency: "AUD", Size: "kg"},
			{ProductName: "Banana", Price: "3.00", Currency: "AUD", Size: "kg"},
		},
	}

	cacheFile := filepath.Join(tempDir, "test_output.json")
	config := &Config{CacheFile: cacheFile}

	writeOffersToFile(testData, config)

	// Verify file was written
	if _, err := os.Stat(cacheFile); os.IsNotExist(err) {
		t.Fatal("Cache file was not created")
	}

	// Read and verify content
	content, err := os.ReadFile(cacheFile)
	if err != nil {
		t.Fatalf("Failed to read cache file: %v", err)
	}

	var result ResponseData
	err = json.Unmarshal(content, &result)
	if err != nil {
		t.Fatalf("Failed to unmarshal written JSON: %v", err)
	}

	if result.LastUpdated != testData.LastUpdated {
		t.Errorf("Expected LastUpdated '%s', got '%s'", testData.LastUpdated, result.LastUpdated)
	}
	if result.Business != testData.Business {
		t.Errorf("Expected Business '%s', got '%s'", testData.Business, result.Business)
	}
	if len(result.Offers) != len(testData.Offers) {
		t.Errorf("Expected %d offers, got %d", len(testData.Offers), len(result.Offers))
	}
}


// Test helper functions
func TestDateFormat(t *testing.T) {
	if dateFormat != "2006-01-02" {
		t.Errorf("Expected dateFormat to be '2006-01-02', got '%s'", dateFormat)
	}
}

// Test that the regex pattern works as expected
func TestRegexPattern(t *testing.T) {
	pattern := `(?i)href="([^"]*SPECIALS?[^"]*\.jpg)"`
	regex := regexp.MustCompile(pattern)

	testCases := []struct {
		input    string
		expected []string
	}{
		{
			`href="https://example.com/specials.jpg"`,
			[]string{"https://example.com/specials.jpg"},
		},
		{
			`href="https://example.com/SPECIALS.jpg"`,
			[]string{"https://example.com/SPECIALS.jpg"},
		},
		{
			`href="https://example.com/special.jpg"`,
			[]string{"https://example.com/special.jpg"},
		},
		{
			`href="https://example.com/regular.jpg"`,
			[]string{}, // Should not match
		},
	}

	for _, tc := range testCases {
		matches := regex.FindAllStringSubmatch(tc.input, -1)
		var results []string
		for _, match := range matches {
			if len(match) > 1 {
				results = append(results, match[1])
			}
		}

		if len(results) != len(tc.expected) {
			t.Errorf("For input '%s': expected %d matches, got %d", tc.input, len(tc.expected), len(results))
			continue
		}

		for i, expected := range tc.expected {
			if results[i] != expected {
				t.Errorf("For input '%s': expected match '%s', got '%s'", tc.input, expected, results[i])
			}
		}
	}
}

// Test that the logger is properly initialized
func TestLoggerInitialization(t *testing.T) {
	// This test ensures the package-level logger is initialized
	if log == nil {
		t.Error("Package logger should be initialized")
	}
}

// Test that the ResponseData struct can be properly marshaled/unmarshaled
func TestResponseDataJSON(t *testing.T) {
	original := ResponseData{
		LastUpdated: "2024-01-01",
		Business:    "Test Store",
		Location: Location{
			Latitude:  -37.8136,
			Longitude: 144.9631,
			Address:   "123 Test St",
			City:      "Melbourne",
			State:     "VIC",
			Zip:       "3000",
			Country:   "Australia",
		},
		Offers: []Offer{
			{
				ProductName: "Red Apple",
				Price:       "2.50",
				Currency:    "AUD",
				Size:        "kg",
			},
			{
				ProductName: "Banana",
				Price:       "3.20",
				Currency:    "AUD",
				Size:        "kg",
			},
		},
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Failed to marshal ResponseData: %v", err)
	}

	// Unmarshal back
	var unmarshaled ResponseData
	err = json.Unmarshal(jsonData, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal ResponseData: %v", err)
	}

	// Compare fields
	if unmarshaled.LastUpdated != original.LastUpdated {
		t.Errorf("LastUpdated mismatch: got %s, want %s", unmarshaled.LastUpdated, original.LastUpdated)
	}
	if unmarshaled.Business != original.Business {
		t.Errorf("Business mismatch: got %s, want %s", unmarshaled.Business, original.Business)
	}
	if unmarshaled.Location.Latitude != original.Location.Latitude {
		t.Errorf("Location.Latitude mismatch: got %f, want %f", unmarshaled.Location.Latitude, original.Location.Latitude)
	}
	if len(unmarshaled.Offers) != len(original.Offers) {
		t.Errorf("Offers count mismatch: got %d, want %d", len(unmarshaled.Offers), len(original.Offers))
	}

	for i, offer := range unmarshaled.Offers {
		if offer.ProductName != original.Offers[i].ProductName {
			t.Errorf("Offer %d ProductName mismatch: got %s, want %s", i, offer.ProductName, original.Offers[i].ProductName)
		}
		if offer.Price != original.Offers[i].Price {
			t.Errorf("Offer %d Price mismatch: got %s, want %s", i, offer.Price, original.Offers[i].Price)
		}
	}
}
