package internal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"regexp"
	"sync"
	"time"
	_ "time/tzdata"
)

const dateFormat = "2006-01-02"

var log = slog.New(slog.NewTextHandler(os.Stderr, nil))

// retryWithBackoff executes a function with exponential backoff retry logic
func retryWithBackoff(ctx context.Context, config *Config, operation string, logger *slog.Logger, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		if attempt > 0 {
			// Calculate exponential backoff delay
			delay := time.Duration(float64(config.RetryBaseDelay) * math.Pow(2, float64(attempt-1)))
			logger.Info("Retrying operation after delay",
				"operation", operation,
				"attempt", attempt,
				"max_retries", config.MaxRetries,
				"delay", delay,
				"last_error", lastErr)

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
				// Continue to next attempt
			}
		}

		err := fn()
		if err == nil {
			if attempt > 0 {
				logger.Info("Operation succeeded after retry",
					"operation", operation,
					"attempts", attempt+1)
			}
			return nil
		}

		lastErr = err
		logger.Warn("Operation failed, will retry",
			"operation", operation,
			"attempt", attempt,
			"max_retries", config.MaxRetries,
			"error", err)
	}

	logger.Error("Operation failed after all retries",
		"operation", operation,
		"attempts", config.MaxRetries+1,
		"final_error", lastErr)
	return lastErr
}

func FetchBigWatermelonDailyDeals(config *Config) ResponseData {
	logger := log.With("operation", "fetch_deals", "business", config.BusinessName)
	return fetchBigWatermelonDailyDealsWithLogger(config, logger)
}

func fetchBigWatermelonDailyDealsWithLogger(config *Config, logger *slog.Logger) ResponseData {
	logger.Info("Starting deals fetch operation")

	ctx, cancel := context.WithTimeout(context.Background(), config.OverallTimeout)
	defer cancel()

	logger.Debug("Checking local cache file", "cache_file", config.CacheFile)
	localResp := checkLocalFileWithLogger(config, logger)

	currentDate := time.Now().Format(dateFormat)
	if localResp.LastUpdated == currentDate {
		logger.Info("Cache is up to date, returning cached data",
			"cached_date", localResp.LastUpdated,
			"offers_count", len(localResp.Offers))
		return localResp
	}

	logger.Info("Cache is stale or missing, fetching fresh data",
		"cached_date", localResp.LastUpdated,
		"current_date", currentDate)

	localTime, err := time.LoadLocation(config.Timezone)
	if err != nil {
		logger.Error("Failed to load timezone location", "timezone", config.Timezone, "error", err)
		return ResponseData{}
	}

	currentHour := time.Now().In(localTime).Hour()
	if currentHour < config.FetchHour {
		logger.Info("Too early to fetch deals, waiting for configured hour",
			"current_hour", currentHour,
			"required_hour", config.FetchHour,
			"timezone", config.Timezone)
		return ResponseData{
			LastUpdated: currentDate,
			Business:    config.BusinessName,
			Location:    config.Location,
		}
	}

	logger.Info("Downloading images from Big Watermelon")
	images := downloadImagesFromBigWatermelonWithLogger(config, logger)

	if len(images) == 0 {
		logger.Warn("No images downloaded, returning empty response")
		return ResponseData{
			LastUpdated: currentDate,
			Business:    config.BusinessName,
			Location:    config.Location,
		}
	}

	logger.Info("Processing images with Requesty AI", "image_count", len(images))
	offers, err := makeRequestyAPICall(ctx, config, images, logger)
	if err != nil {
		logger.Error("Failed to process images with Requesty AI", "error", err)
		return ResponseData{
			LastUpdated: currentDate,
			Business:    config.BusinessName,
			Location:    config.Location,
		}
	}

	resp := ResponseData{
		LastUpdated: currentDate,
		Business:    config.BusinessName,
		Location:    config.Location,
		Offers:      offers,
	}

	logger.Info("Writing results to cache file", "offers_count", len(offers))
	writeOffersToFileWithLogger(resp, config, logger)

	logger.Info("Deals fetch operation completed successfully",
		"total_offers", len(offers))

	return resp
}

func checkLocalFile(config *Config) ResponseData {
	logger := log.With("operation", "check_cache", "cache_file", config.CacheFile)
	return checkLocalFileWithLogger(config, logger)
}

func checkLocalFileWithLogger(config *Config, logger *slog.Logger) ResponseData {
	if _, err := os.Stat(config.CacheFile); errors.Is(err, os.ErrNotExist) {
		logger.Debug("Cache file does not exist")
		return ResponseData{}
	}

	logger.Debug("Reading cache file")
	content, err := os.ReadFile(config.CacheFile)
	if err != nil {
		logger.Error("Failed to read cache file", "error", err)
		return ResponseData{}
	}

	logger.Debug("Parsing cache file JSON", "content_size", len(content))
	var resp ResponseData
	if err := json.Unmarshal(content, &resp); err != nil {
		logger.Error("Failed to unmarshal cache JSON", "error", err)
		return ResponseData{}
	}

	logger.Debug("Cache loaded successfully",
		"cached_date", resp.LastUpdated,
		"offers_count", len(resp.Offers))

	return resp
}

func downloadImagesFromBigWatermelon(config *Config) [][]byte {
	logger := log.With("operation", "download_images", "url", config.SpecialsURL)
	return downloadImagesFromBigWatermelonWithLogger(config, logger)
}

func downloadImagesFromBigWatermelonWithLogger(config *Config, logger *slog.Logger) [][]byte {
	var imageList [][]byte
	var mu sync.Mutex

	logger.Info("Starting image download from Big Watermelon")

	logger.Debug("Fetching specials page HTML")
	resp, err := config.HTTPClient.Get(config.SpecialsURL)
	if err != nil {
		logger.Error("Failed to fetch specials page", "error", err)
		return imageList
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			logger.Error("Error closing response body", "error", err)
		}
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		logger.Error("Specials page returned non-200 status",
			"status_code", resp.StatusCode,
			"url", config.SpecialsURL)
		return imageList
	}

	logger.Debug("Reading HTML content")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Failed to read response body", "error", err)
		return imageList
	}

	htmlContent := string(body)
	logger.Debug("HTML content retrieved", "content_length", len(htmlContent))

	regex := regexp.MustCompile(`(?i)href="([^"]*SPECIALS?[^"]*\.jpg)"`)
	matches := regex.FindAllStringSubmatch(htmlContent, -1)

	if len(matches) == 0 {
		logger.Warn("No SPECIALS images found in HTML content")
		return imageList
	}

	logger.Info("Found SPECIALS image URLs", "count", len(matches))
	for i, match := range matches {
		logger.Debug("Found image URL", "index", i, "url", match[1])
	}

	var wg sync.WaitGroup
	wg.Add(len(matches))
	successCount := 0
	errorCount := 0

	for i, match := range matches {
		go func(index int, imageURL string) {
			defer wg.Done()

			logger.Debug("Downloading image", "index", index, "url", imageURL)

			imageResp, err := config.HTTPClient.Get(imageURL)
			if err != nil {
				logger.Error("Failed to download image",
					"index", index,
					"url", imageURL,
					"error", err)
				mu.Lock()
				errorCount++
				mu.Unlock()
				return
			}

			defer func(Body io.ReadCloser) {
				err := Body.Close()
				if err != nil {
					logger.Error("Error closing image response body",
						"index", index,
						"error", err)
				}
			}(imageResp.Body)

			if imageResp.StatusCode != http.StatusOK {
				logger.Error("Image download returned non-200 status",
					"index", index,
					"url", imageURL,
					"status_code", imageResp.StatusCode)
				mu.Lock()
				errorCount++
				mu.Unlock()
				return
			}

			imageData, err := io.ReadAll(imageResp.Body)
			if err != nil {
				logger.Error("Failed to read image data",
					"index", index,
					"url", imageURL,
					"error", err)
				mu.Lock()
				errorCount++
				mu.Unlock()
				return
			}

			logger.Debug("Image downloaded successfully",
				"index", index,
				"size_bytes", len(imageData))

			mu.Lock()
			imageList = append(imageList, imageData)
			successCount++
			mu.Unlock()
		}(i, match[1])
	}

	wg.Wait()

	logger.Info("Image download completed",
		"total_urls", len(matches),
		"successful_downloads", successCount,
		"failed_downloads", errorCount)

	return imageList
}

func writeOffersToFile(resp ResponseData, config *Config) {
	logger := log.With("operation", "write_cache", "cache_file", config.CacheFile)
	writeOffersToFileWithLogger(resp, config, logger)
}

func writeOffersToFileWithLogger(resp ResponseData, config *Config, logger *slog.Logger) {
	logger.Info("Writing response data to cache file",
		"offers_count", len(resp.Offers),
		"last_updated", resp.LastUpdated)

	jsonData, err := json.MarshalIndent(resp, "", "\t")
	if err != nil {
		logger.Error("Failed to marshal response data to JSON", "error", err)
		return
	}

	logger.Debug("JSON marshaling successful", "json_size_bytes", len(jsonData))

	err = os.WriteFile(config.CacheFile, jsonData, 0644)
	if err != nil {
		logger.Error("Failed to write cache file", "error", err)
		return
	}

	logger.Info("Cache file written successfully", "file_path", config.CacheFile)
}
