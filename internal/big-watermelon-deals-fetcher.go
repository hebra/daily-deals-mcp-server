package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"regexp"
	"strings"
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

	logger.Info("Initializing Gemini client")
	client := getClientWithLogger(ctx, logger)
	if client == nil {
		logger.Error("Failed to initialize Gemini client")
		return ResponseData{}
	}
	defer func(client *genai.Client) {
		err := client.Close()
		if err != nil {
			logger.Error("Error closing Gemini client", "error", err)
		}
	}(client)

	logger.Info("Cleaning up old GCP files")
	cleanUpGcpFilesWithLogger(ctx, client, config, logger)

	logger.Info("Downloading and uploading images from Big Watermelon")
	gcpFiles := downloadAndUploadImagesWithLogger(ctx, client, config, logger)

	logger.Info("Processing images with Gemini AI", "file_count", len(gcpFiles))
	offers := makeRequestToGeminiWithLogger(ctx, client, gcpFiles, config, logger)

	resp := ResponseData{
		LastUpdated: currentDate,
		Business:    config.BusinessName,
		Location:    config.Location,
		Offers:      offers,
	}

	logger.Info("Writing results to cache file", "offers_count", len(offers))
	writeOffersToFileWithLogger(resp, config, logger)

	logger.Info("Deals fetch operation completed successfully",
		"total_offers", len(offers),
		"processing_time", time.Since(time.Now()))

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

func getClient(ctx context.Context) *genai.Client {
	logger := log.With("operation", "init_gemini_client")
	return getClientWithLogger(ctx, logger)
}

func getClientWithLogger(ctx context.Context, logger *slog.Logger) *genai.Client {
	logger.Debug("Initializing Gemini client")
	client, err := genai.NewClient(ctx,
		option.WithAPIKey(os.Getenv("GEMINI_API_KEY")))
	if err != nil {
		logger.Error("Failed to create Gemini client", "error", err)
		return nil
	}
	logger.Debug("Gemini client initialized successfully")
	return client
}

func cleanUpGcpFiles(ctx context.Context, client *genai.Client, config *Config) {
	logger := log.With("operation", "cleanup_gcp", "prefix", config.GCPFilePrefix)
	cleanUpGcpFilesWithLogger(ctx, client, config, logger)
}

func cleanUpGcpFilesWithLogger(ctx context.Context, client *genai.Client, config *Config, logger *slog.Logger) {
	logger.Debug("Starting GCP file cleanup")
	files := client.ListFiles(ctx)
	deletedCount := 0

	for {
		file, err := files.Next()
		if err != nil {
			if errors.Is(err, iterator.Done) {
				break
			}
			logger.Error("Error listing GCP files", "error", err)
			return
		}

		if strings.Contains(file.Name, config.GCPFilePrefix) {
			logger.Debug("Deleting old GCP file", "file_name", file.Name)
			err = client.DeleteFile(ctx, file.Name)
			if err != nil {
				logger.Warn("Failed to delete GCP file", "file_name", file.Name, "error", err)
			} else {
				deletedCount++
			}
		}
	}

	logger.Info("GCP cleanup completed", "files_deleted", deletedCount)
}

func downloadAndUploadImagesWithLogger(ctx context.Context, client *genai.Client, config *Config, logger *slog.Logger) []*genai.File {
	var files []*genai.File
	var mu sync.Mutex

	logger.Info("Starting download and upload of images")

	httpClient := &http.Client{Timeout: config.HTTPTimeout}

	resp, err := httpClient.Get(config.SpecialsURL)
	if err != nil {
		logger.Error("Failed to fetch specials page", "error", err)
		return files
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Error("Specials page returned non-200 status", "status_code", resp.StatusCode)
		return files
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Failed to read response body", "error", err)
		return files
	}

	htmlContent := string(body)
	regex := regexp.MustCompile(`(?i)href="([^"]*SPECIALS?[^"]*\.jpg)"`)
	matches := regex.FindAllStringSubmatch(htmlContent, -1)

	if len(matches) == 0 {
		logger.Warn("No SPECIALS images found")
		return files
	}

	logger.Info("Found image URLs", "count", len(matches))

	semaphore := make(chan struct{}, config.MaxConcurrentGoroutines)
	var wg sync.WaitGroup
	wg.Add(len(matches))
	successCount := 0
	errorCount := 0

	for i, match := range matches {
		go func(index int, imageURL string) {
			defer wg.Done()
			<-semaphore
			defer func() { semaphore <- struct{}{} }()

			logger.Debug("Processing image", "index", index, "url", imageURL)

			imageResp, err := httpClient.Get(imageURL)
			if err != nil {
				logger.Error("Failed to download image", "index", index, "url", imageURL, "error", err)
				mu.Lock()
				errorCount++
				mu.Unlock()
				return
			}
			defer imageResp.Body.Close()

			if imageResp.StatusCode != http.StatusOK {
				logger.Error("Image download non-200", "index", index, "status", imageResp.StatusCode)
				mu.Lock()
				errorCount++
				mu.Unlock()
				return
			}

			imageName := config.GCPFilePrefix + fmt.Sprintf("%d-jpg", index)
			options := genai.UploadFileOptions{
				DisplayName: imageName,
				MIMEType:    "image/jpeg",
			}

			file, err := client.UploadFile(ctx, imageName, imageResp.Body, &options)
			if err != nil {
				logger.Error("Failed to upload image", "index", index, "name", imageName, "error", err)
				mu.Lock()
				errorCount++
				mu.Unlock()
				return
			}

			logger.Debug("Uploaded image", "index", index, "file_uri", file.URI)

			mu.Lock()
			files = append(files, file)
			successCount++
			mu.Unlock()
		}(i, match[1])
	}

	wg.Wait()

	logger.Info("Download and upload completed", "total", len(matches), "success", successCount, "errors", errorCount)

	return files
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

func uploadImagesToGoogleCloud(ctx context.Context, client *genai.Client, images [][]byte, config *Config) []*genai.File {
	logger := log.With("operation", "upload_images", "image_count", len(images))
	return uploadImagesToGoogleCloudWithLogger(ctx, client, images, config, logger)
}

func uploadImagesToGoogleCloudWithLogger(ctx context.Context, client *genai.Client, images [][]byte, config *Config, logger *slog.Logger) []*genai.File {
	var files []*genai.File
	var mu sync.Mutex

	if len(images) == 0 {
		logger.Warn("No images to upload")
		return files
	}

	logger.Info("Starting image uploads to Google Cloud")

	var wg sync.WaitGroup
	wg.Add(len(images))
	successCount := 0
	errorCount := 0

	for imageIndex, image := range images {
		go func(index int, imageData []byte) {
			defer wg.Done()

			if len(imageData) == 0 {
				logger.Error("Empty image data", "index", index)
				mu.Lock()
				errorCount++
				mu.Unlock()
				return
			}

			imageName := config.GCPFilePrefix + fmt.Sprintf("%d-jpg", index)
			logger.Debug("Uploading image to GCP",
				"index", index,
				"name", imageName,
				"size_bytes", len(imageData))

			reader := bytes.NewReader(imageData)
			options := genai.UploadFileOptions{
				DisplayName: imageName,
				MIMEType:    "image/jpeg",
			}

			file, err := client.UploadFile(ctx, imageName, reader, &options)
			if err != nil {
				logger.Error("Failed to upload image to Gemini",
					"index", index,
					"name", imageName,
					"error", err)
				mu.Lock()
				errorCount++
				mu.Unlock()
				return
			}

			logger.Debug("Image uploaded successfully",
				"index", index,
				"file_uri", file.URI)

			mu.Lock()
			files = append(files, file)
			successCount++
			mu.Unlock()
		}(imageIndex, image)
	}

	wg.Wait()

	logger.Info("Image upload completed",
		"total_images", len(images),
		"successful_uploads", successCount,
		"failed_uploads", errorCount)

	return files
}

func makeRequestToGemini(ctx context.Context, client *genai.Client, files []*genai.File, config *Config) []Offer {
	logger := log.With("operation", "gemini_processing", "file_count", len(files))
	return makeRequestToGeminiWithLogger(ctx, client, files, config, logger)
}

func makeRequestToGeminiWithLogger(ctx context.Context, client *genai.Client, files []*genai.File, config *Config, logger *slog.Logger) []Offer {
	var offers []Offer
	var mu sync.Mutex

	if len(files) == 0 {
		logger.Warn("No files to process with Gemini")
		return offers
	}

	logger.Info("Starting Gemini AI processing for image analysis")

	// Create a timeout context for Gemini operations
	geminiCtx, geminiCancel := context.WithTimeout(ctx, config.GeminiTimeout)
	defer geminiCancel()

	semaphore := make(chan struct{}, config.MaxConcurrentGoroutines)
	var wg sync.WaitGroup
	wg.Add(len(files))
	successCount := 0
	errorCount := 0

	for _, file := range files {
		go func(gcpFile *genai.File) {
			defer wg.Done()
			<-semaphore
			defer func() { semaphore <- struct{}{} }()
		go func(gcpFile *genai.File) {
			defer wg.Done()

			fileLogger := logger.With("file_name", gcpFile.Name, "file_uri", gcpFile.URI)
			fileLogger.Debug("Processing image with Gemini AI")

			// Ensure file cleanup happens
			defer func(client *genai.Client, ctx context.Context, name string) {
				err := client.DeleteFile(ctx, name)
				if err != nil {
					fileLogger.Error("Failed to delete GCP file after processing", "error", err)
				} else {
					fileLogger.Debug("GCP file deleted successfully")
				}
			}(client, geminiCtx, gcpFile.Name)

			genModel := client.GenerativeModel(config.GeminiModel)
			genModel.ResponseMIMEType = "application/json"

			prompt := `The image is an advertisement for fruits and vegetables that are on sale.
Offers are separated by thing vertical and horizontal black lines.
There are one, two or three offer columns per row.
The name and price of the fruits are in the right lower corner of each row.
Please extract the name and price of each offer from the image.
Split each item into product name, price, currency and optionally the packaging type (e.g. ea, pk, kg etc.).
Normalize the product names to start with upper case letters and the rest lower case letters.
For the result use this JSON schema:
Offer = {'productName': string, 'price': number, 'currency': string, 'size': string}
Return: Array<Offer>`

			var resp *genai.GenerateContentResponse
			var err error
			err = retryWithBackoff(geminiCtx, config, fmt.Sprintf("gemini_api_%s", gcpFile.Name), fileLogger, func() error {
				var apiErr error
				resp, apiErr = genModel.GenerateContent(geminiCtx,
					genai.FileData{URI: gcpFile.URI},
					genai.Text(prompt))
				return apiErr
			})

			if err != nil {
				fileLogger.Error("Gemini API call failed after retries", "error", err)
				mu.Lock()
				errorCount++
				mu.Unlock()
				return
			}

			fileLogger.Debug("Gemini API call successful, parsing response")
			parsedOffers := parseResponseJsonWithLogger(resp, fileLogger)

			if len(parsedOffers) > 0 {
				fileLogger.Info("Successfully extracted offers from image",
					"offers_count", len(parsedOffers))
			} else {
				fileLogger.Warn("No offers extracted from image")
			}

			mu.Lock()
			offers = append(offers, parsedOffers...)
			successCount++
			mu.Unlock()
		}(file)
	}

	wg.Wait()

	logger.Info("Gemini processing completed",
		"total_files", len(files),
		"successful_processing", successCount,
		"failed_processing", errorCount,
		"total_offers_extracted", len(offers))

	return offers
}

func parseResponseJson(resp *genai.GenerateContentResponse) []Offer {
	logger := log.With("operation", "parse_gemini_response")
	return parseResponseJsonWithLogger(resp, logger)
}

func parseResponseJsonWithLogger(resp *genai.GenerateContentResponse, logger *slog.Logger) []Offer {
	if resp == nil {
		logger.Error("Received nil response from Gemini")
		return []Offer{}
	}

	logger.Debug("Parsing Gemini response", "candidates_count", len(resp.Candidates))

	for candidateIndex, candidate := range resp.Candidates {
		logger.Debug("Processing candidate", "index", candidateIndex, "parts_count", len(candidate.Content.Parts))

		for partIndex, part := range candidate.Content.Parts {
			var offers []Offer

			rawJson, ok := part.(genai.Text)
			if !ok {
				logger.Warn("Unexpected part type in Gemini response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"part_type", fmt.Sprintf("%T", part))
				continue
			}

			logger.Debug("Attempting to parse JSON response",
				"candidate_index", candidateIndex,
				"part_index", partIndex,
				"json_length", len(rawJson))

			if err := json.Unmarshal([]byte(rawJson), &offers); err != nil {
				logger.Error("Failed to unmarshal Gemini JSON response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"error", err,
					"raw_json", string(rawJson))
				continue
			}

			if len(offers) > 0 {
				logger.Info("Successfully parsed offers from Gemini response",
					"offers_count", len(offers))
				for i, offer := range offers {
					logger.Debug("Parsed offer",
						"index", i,
						"product", offer.ProductName,
						"price", offer.Price,
						"currency", offer.Currency,
						"size", offer.Size)
				}
			} else {
				logger.Warn("Gemini response contained no offers")
			}

			return offers
		}
	}

	logger.Warn("No valid offers found in Gemini response")
	return []Offer{}
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

	logger.Info("Gemini processing completed",
		"total_files", len(files),
		"successful_processing", successCount,
		"failed_processing", errorCount,
		"total_offers_extracted", len(offers))

	return offers
}

func parseResponseJson(resp *genai.GenerateContentResponse) []Offer {
	logger := log.With("operation", "parse_gemini_response")
	return parseResponseJsonWithLogger(resp, logger)
}

func parseResponseJsonWithLogger(resp *genai.GenerateContentResponse, logger *slog.Logger) []Offer {
	if resp == nil {
		logger.Error("Received nil response from Gemini")
		return []Offer{}
	}

	logger.Debug("Parsing Gemini response", "candidates_count", len(resp.Candidates))

	for candidateIndex, candidate := range resp.Candidates {
		logger.Debug("Processing candidate", "index", candidateIndex, "parts_count", len(candidate.Content.Parts))

		for partIndex, part := range candidate.Content.Parts {
			var offers []Offer

			rawJson, ok := part.(genai.Text)
			if !ok {
				logger.Warn("Unexpected part type in Gemini response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"part_type", fmt.Sprintf("%T", part))
				continue
			}

			logger.Debug("Attempting to parse JSON response",
				"candidate_index", candidateIndex,
				"part_index", partIndex,
				"json_length", len(rawJson))

			if err := json.Unmarshal([]byte(rawJson), &offers); err != nil {
				logger.Error("Failed to unmarshal Gemini JSON response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"error", err,
					"raw_json", string(rawJson))
				continue
			}

			if len(offers) > 0 {
				logger.Info("Successfully parsed offers from Gemini response",
					"offers_count", len(offers))
				for i, offer := range offers {
					logger.Debug("Parsed offer",
						"index", i,
						"product", offer.ProductName,
						"price", offer.Price,
						"currency", offer.Currency,
						"size", offer.Size)
				}
			} else {
				logger.Warn("Gemini response contained no offers")
			}

			return offers
		}
	}

	logger.Warn("No valid offers found in Gemini response")
	return []Offer{}
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

	logger.Info("Gemini processing completed",
		"total_files", len(files),
		"successful_processing", successCount,
		"failed_processing", errorCount,
		"total_offers_extracted", len(offers))

	return offers
}

func parseResponseJson(resp *genai.GenerateContentResponse) []Offer {
	logger := log.With("operation", "parse_gemini_response")
	return parseResponseJsonWithLogger(resp, logger)
}

func parseResponseJsonWithLogger(resp *genai.GenerateContentResponse, logger *slog.Logger) []Offer {
	if resp == nil {
		logger.Error("Received nil response from Gemini")
		return []Offer{}
	}

	logger.Debug("Parsing Gemini response", "candidates_count", len(resp.Candidates))

	for candidateIndex, candidate := range resp.Candidates {
		logger.Debug("Processing candidate", "index", candidateIndex, "parts_count", len(candidate.Content.Parts))

		for partIndex, part := range candidate.Content.Parts {
			var offers []Offer

			rawJson, ok := part.(genai.Text)
			if !ok {
				logger.Warn("Unexpected part type in Gemini response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"part_type", fmt.Sprintf("%T", part))
				continue
			}

			logger.Debug("Attempting to parse JSON response",
				"candidate_index", candidateIndex,
				"part_index", partIndex,
				"json_length", len(rawJson))

			if err := json.Unmarshal([]byte(rawJson), &offers); err != nil {
				logger.Error("Failed to unmarshal Gemini JSON response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"error", err,
					"raw_json", string(rawJson))
				continue
			}

			if len(offers) > 0 {
				logger.Info("Successfully parsed offers from Gemini response",
					"offers_count", len(offers))
				for i, offer := range offers {
					logger.Debug("Parsed offer",
						"index", i,
						"product", offer.ProductName,
						"price", offer.Price,
						"currency", offer.Currency,
						"size", offer.Size)
				}
			} else {
				logger.Warn("Gemini response contained no offers")
			}

			return offers
		}
	}

	logger.Warn("No valid offers found in Gemini response")
	return []Offer{}
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

		"successful_processing", successCount,
		"failed_processing", errorCount,
		"total_offers_extracted", len(offers))

	return offers
}

func parseResponseJson(resp *genai.GenerateContentResponse) []Offer {
	logger := log.With("operation", "parse_gemini_response")
	return parseResponseJsonWithLogger(resp, logger)
}

func parseResponseJsonWithLogger(resp *genai.GenerateContentResponse, logger *slog.Logger) []Offer {
	if resp == nil {
		logger.Error("Received nil response from Gemini")
		return []Offer{}
	}

	logger.Debug("Parsing Gemini response", "candidates_count", len(resp.Candidates))

	for candidateIndex, candidate := range resp.Candidates {
		logger.Debug("Processing candidate", "index", candidateIndex, "parts_count", len(candidate.Content.Parts))

		for partIndex, part := range candidate.Content.Parts {
			var offers []Offer

			rawJson, ok := part.(genai.Text)
			if !ok {
				logger.Warn("Unexpected part type in Gemini response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"part_type", fmt.Sprintf("%T", part))
				continue
			}

			logger.Debug("Attempting to parse JSON response",
				"candidate_index", candidateIndex,
				"part_index", partIndex,
				"json_length", len(rawJson))

			if err := json.Unmarshal([]byte(rawJson), &offers); err != nil {
				logger.Error("Failed to unmarshal Gemini JSON response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"error", err,
					"raw_json", string(rawJson))
				continue
			}

			if len(offers) > 0 {
				logger.Info("Successfully parsed offers from Gemini response",
					"offers_count", len(offers))
				for i, offer := range offers {
					logger.Debug("Parsed offer",
						"index", i,
						"product", offer.ProductName,
						"price", offer.Price,
						"currency", offer.Currency,
						"size", offer.Size)
				}
			} else {
				logger.Warn("Gemini response contained no offers")
			}

			return offers
		}
	}

	logger.Warn("No valid offers found in Gemini response")
	return []Offer{}
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

	logger.Info("Gemini processing completed",
		"total_files", len(files),
		"successful_processing", successCount,
		"failed_processing", errorCount,
		"total_offers_extracted", len(offers))

	return offers
}

func parseResponseJson(resp *genai.GenerateContentResponse) []Offer {
	logger := log.With("operation", "parse_gemini_response")
	return parseResponseJsonWithLogger(resp, logger)
}

func parseResponseJsonWithLogger(resp *genai.GenerateContentResponse, logger *slog.Logger) []Offer {
	if resp == nil {
		logger.Error("Received nil response from Gemini")
		return []Offer{}
	}

	logger.Debug("Parsing Gemini response", "candidates_count", len(resp.Candidates))

	for candidateIndex, candidate := range resp.Candidates {
		logger.Debug("Processing candidate", "index", candidateIndex, "parts_count", len(candidate.Content.Parts))

		for partIndex, part := range candidate.Content.Parts {
			var offers []Offer

			rawJson, ok := part.(genai.Text)
			if !ok {
				logger.Warn("Unexpected part type in Gemini response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"part_type", fmt.Sprintf("%T", part))
				continue
			}

			logger.Debug("Attempting to parse JSON response",
				"candidate_index", candidateIndex,
				"part_index", partIndex,
				"json_length", len(rawJson))

			if err := json.Unmarshal([]byte(rawJson), &offers); err != nil {
				logger.Error("Failed to unmarshal Gemini JSON response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"error", err,
					"raw_json", string(rawJson))
				continue
			}

			if len(offers) > 0 {
				logger.Info("Successfully parsed offers from Gemini response",
					"offers_count", len(offers))
				for i, offer := range offers {
					logger.Debug("Parsed offer",
						"index", i,
						"product", offer.ProductName,
						"price", offer.Price,
						"currency", offer.Currency,
						"size", offer.Size)
				}
			} else {
				logger.Warn("Gemini response contained no offers")
			}

			return offers
		}
	}

	logger.Warn("No valid offers found in Gemini response")
	return []Offer{}
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

	logger.Info("Gemini processing completed",
		"total_files", len(files),
		"successful_processing", successCount,
		"failed_processing", errorCount,
		"total_offers_extracted", len(offers))

	return offers
}

func parseResponseJson(resp *genai.GenerateContentResponse) []Offer {
	logger := log.With("operation", "parse_gemini_response")
	return parseResponseJsonWithLogger(resp, logger)
}

func parseResponseJsonWithLogger(resp *genai.GenerateContentResponse, logger *slog.Logger) []Offer {
	if resp == nil {
		logger.Error("Received nil response from Gemini")
		return []Offer{}
	}

	logger.Debug("Parsing Gemini response", "candidates_count", len(resp.Candidates))

	for candidateIndex, candidate := range resp.Candidates {
		logger.Debug("Processing candidate", "index", candidateIndex, "parts_count", len(candidate.Content.Parts))

		for partIndex, part := range candidate.Content.Parts {
			var offers []Offer

			rawJson, ok := part.(genai.Text)
			if !ok {
				logger.Warn("Unexpected part type in Gemini response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"part_type", fmt.Sprintf("%T", part))
				continue
			}

			logger.Debug("Attempting to parse JSON response",
				"candidate_index", candidateIndex,
				"part_index", partIndex,
				"json_length", len(rawJson))

			if err := json.Unmarshal([]byte(rawJson), &offers); err != nil {
				logger.Error("Failed to unmarshal Gemini JSON response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"error", err,
					"raw_json", string(rawJson))
				continue
			}

			if len(offers) > 0 {
				logger.Info("Successfully parsed offers from Gemini response",
					"offers_count", len(offers))
				for i, offer := range offers {
					logger.Debug("Parsed offer",
						"index", i,
						"product", offer.ProductName,
						"price", offer.Price,
						"currency", offer.Currency,
						"size", offer.Size)
				}
			} else {
				logger.Warn("Gemini response contained no offers")
			}

			return offers
		}
	}

	logger.Warn("No valid offers found in Gemini response")
	return []Offer{}
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


		"failed_processing", errorCount,
		"total_offers_extracted", len(offers))

	return offers
}

func parseResponseJson(resp *genai.GenerateContentResponse) []Offer {
	logger := log.With("operation", "parse_gemini_response")
	return parseResponseJsonWithLogger(resp, logger)
}

func parseResponseJsonWithLogger(resp *genai.GenerateContentResponse, logger *slog.Logger) []Offer {
	if resp == nil {
		logger.Error("Received nil response from Gemini")
		return []Offer{}
	}

	logger.Debug("Parsing Gemini response", "candidates_count", len(resp.Candidates))

	for candidateIndex, candidate := range resp.Candidates {
		logger.Debug("Processing candidate", "index", candidateIndex, "parts_count", len(candidate.Content.Parts))

		for partIndex, part := range candidate.Content.Parts {
			var offers []Offer

			rawJson, ok := part.(genai.Text)
			if !ok {
				logger.Warn("Unexpected part type in Gemini response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"part_type", fmt.Sprintf("%T", part))
				continue
			}

			logger.Debug("Attempting to parse JSON response",
				"candidate_index", candidateIndex,
				"part_index", partIndex,
				"json_length", len(rawJson))

			if err := json.Unmarshal([]byte(rawJson), &offers); err != nil {
				logger.Error("Failed to unmarshal Gemini JSON response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"error", err,
					"raw_json", string(rawJson))
				continue
			}

			if len(offers) > 0 {
				logger.Info("Successfully parsed offers from Gemini response",
					"offers_count", len(offers))
				for i, offer := range offers {
					logger.Debug("Parsed offer",
						"index", i,
						"product", offer.ProductName,
						"price", offer.Price,
						"currency", offer.Currency,
						"size", offer.Size)
				}
			} else {
				logger.Warn("Gemini response contained no offers")
			}

			return offers
		}
	}

	logger.Warn("No valid offers found in Gemini response")
	return []Offer{}
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

	logger.Info("Gemini processing completed",
		"total_files", len(files),
		"successful_processing", successCount,
		"failed_processing", errorCount,
		"total_offers_extracted", len(offers))

	return offers
}

func parseResponseJson(resp *genai.GenerateContentResponse) []Offer {
	logger := log.With("operation", "parse_gemini_response")
	return parseResponseJsonWithLogger(resp, logger)
}

func parseResponseJsonWithLogger(resp *genai.GenerateContentResponse, logger *slog.Logger) []Offer {
	if resp == nil {
		logger.Error("Received nil response from Gemini")
		return []Offer{}
	}

	logger.Debug("Parsing Gemini response", "candidates_count", len(resp.Candidates))

	for candidateIndex, candidate := range resp.Candidates {
		logger.Debug("Processing candidate", "index", candidateIndex, "parts_count", len(candidate.Content.Parts))

		for partIndex, part := range candidate.Content.Parts {
			var offers []Offer

			rawJson, ok := part.(genai.Text)
			if !ok {
				logger.Warn("Unexpected part type in Gemini response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"part_type", fmt.Sprintf("%T", part))
				continue
			}

			logger.Debug("Attempting to parse JSON response",
				"candidate_index", candidateIndex,
				"part_index", partIndex,
				"json_length", len(rawJson))

			if err := json.Unmarshal([]byte(rawJson), &offers); err != nil {
				logger.Error("Failed to unmarshal Gemini JSON response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"error", err,
					"raw_json", string(rawJson))
				continue
			}

			if len(offers) > 0 {
				logger.Info("Successfully parsed offers from Gemini response",
					"offers_count", len(offers))
				for i, offer := range offers {
					logger.Debug("Parsed offer",
						"index", i,
						"product", offer.ProductName,
						"price", offer.Price,
						"currency", offer.Currency,
						"size", offer.Size)
				}
			} else {
				logger.Warn("Gemini response contained no offers")
			}

			return offers
		}
	}

	logger.Warn("No valid offers found in Gemini response")
	return []Offer{}
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

	logger.Info("Gemini processing completed",
		"total_files", len(files),
		"successful_processing", successCount,
		"failed_processing", errorCount,
		"total_offers_extracted", len(offers))

	return offers
}

func parseResponseJson(resp *genai.GenerateContentResponse) []Offer {
	logger := log.With("operation", "parse_gemini_response")
	return parseResponseJsonWithLogger(resp, logger)
}

func parseResponseJsonWithLogger(resp *genai.GenerateContentResponse, logger *slog.Logger) []Offer {
	if resp == nil {
		logger.Error("Received nil response from Gemini")
		return []Offer{}
	}

	logger.Debug("Parsing Gemini response", "candidates_count", len(resp.Candidates))

	for candidateIndex, candidate := range resp.Candidates {
		logger.Debug("Processing candidate", "index", candidateIndex, "parts_count", len(candidate.Content.Parts))

		for partIndex, part := range candidate.Content.Parts {
			var offers []Offer

			rawJson, ok := part.(genai.Text)
			if !ok {
				logger.Warn("Unexpected part type in Gemini response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"part_type", fmt.Sprintf("%T", part))
				continue
			}

			logger.Debug("Attempting to parse JSON response",
				"candidate_index", candidateIndex,
				"part_index", partIndex,
				"json_length", len(rawJson))

			if err := json.Unmarshal([]byte(rawJson), &offers); err != nil {
				logger.Error("Failed to unmarshal Gemini JSON response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"error", err,
					"raw_json", string(rawJson))
				continue
			}

			if len(offers) > 0 {
				logger.Info("Successfully parsed offers from Gemini response",
					"offers_count", len(offers))
				for i, offer := range offers {
					logger.Debug("Parsed offer",
						"index", i,
						"product", offer.ProductName,
						"price", offer.Price,
						"currency", offer.Currency,
						"size", offer.Size)
				}
			} else {
				logger.Warn("Gemini response contained no offers")
			}

			return offers
		}
	}

	logger.Warn("No valid offers found in Gemini response")
	return []Offer{}
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


}


	wg.Wait()

	logger.Info("Gemini processing completed",
		"total_files", len(files),
		"successful_processing", successCount,
		"failed_processing", errorCount,
		"total_offers_extracted", len(offers))

	return offers
}

func parseResponseJson(resp *genai.GenerateContentResponse) []Offer {
	logger := log.With("operation", "parse_gemini_response")
	return parseResponseJsonWithLogger(resp, logger)
}

func parseResponseJsonWithLogger(resp *genai.GenerateContentResponse, logger *slog.Logger) []Offer {
	if resp == nil {
		logger.Error("Received nil response from Gemini")
		return []Offer{}
	}

	logger.Debug("Parsing Gemini response", "candidates_count", len(resp.Candidates))

	for candidateIndex, candidate := range resp.Candidates {
		logger.Debug("Processing candidate", "index", candidateIndex, "parts_count", len(candidate.Content.Parts))

		for partIndex, part := range candidate.Content.Parts {
			var offers []Offer

			rawJson, ok := part.(genai.Text)
			if !ok {
				logger.Warn("Unexpected part type in Gemini response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"part_type", fmt.Sprintf("%T", part))
				continue
			}

			logger.Debug("Attempting to parse JSON response",
				"candidate_index", candidateIndex,
				"part_index", partIndex,
				"json_length", len(rawJson))

			if err := json.Unmarshal([]byte(rawJson), &offers); err != nil {
				logger.Error("Failed to unmarshal Gemini JSON response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"error", err,
					"raw_json", string(rawJson))
				continue
			}

			if len(offers) > 0 {
				logger.Info("Successfully parsed offers from Gemini response",
					"offers_count", len(offers))
				for i, offer := range offers {
					logger.Debug("Parsed offer",
						"index", i,
						"product", offer.ProductName,
						"price", offer.Price,
						"currency", offer.Currency,
						"size", offer.Size)
				}
			} else {
				logger.Warn("Gemini response contained no offers")
			}

			return offers
		}
	}

	logger.Warn("No valid offers found in Gemini response")
	return []Offer{}
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

	logger.Info("Gemini processing completed",
		"total_files", len(files),
		"successful_processing", successCount,
		"failed_processing", errorCount,
		"total_offers_extracted", len(offers))

	return offers
}

func parseResponseJson(resp *genai.GenerateContentResponse) []Offer {
	logger := log.With("operation", "parse_gemini_response")
	return parseResponseJsonWithLogger(resp, logger)
}

func parseResponseJsonWithLogger(resp *genai.GenerateContentResponse, logger *slog.Logger) []Offer {
	if resp == nil {
		logger.Error("Received nil response from Gemini")
		return []Offer{}
	}

	logger.Debug("Parsing Gemini response", "candidates_count", len(resp.Candidates))

	for candidateIndex, candidate := range resp.Candidates {
		logger.Debug("Processing candidate", "index", candidateIndex, "parts_count", len(candidate.Content.Parts))

		for partIndex, part := range candidate.Content.Parts {
			var offers []Offer

			rawJson, ok := part.(genai.Text)
			if !ok {
				logger.Warn("Unexpected part type in Gemini response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"part_type", fmt.Sprintf("%T", part))
				continue
			}

			logger.Debug("Attempting to parse JSON response",
				"candidate_index", candidateIndex,
				"part_index", partIndex,
				"json_length", len(rawJson))

			if err := json.Unmarshal([]byte(rawJson), &offers); err != nil {
				logger.Error("Failed to unmarshal Gemini JSON response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"error", err,
					"raw_json", string(rawJson))
				continue
			}

			if len(offers) > 0 {
				logger.Info("Successfully parsed offers from Gemini response",
					"offers_count", len(offers))
				for i, offer := range offers {
					logger.Debug("Parsed offer",
						"index", i,
						"product", offer.ProductName,
						"price", offer.Price,
						"currency", offer.Currency,
						"size", offer.Size)
				}
			} else {
				logger.Warn("Gemini response contained no offers")
			}

			return offers
		}
	}

	logger.Warn("No valid offers found in Gemini response")
	return []Offer{}
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

	logger.Info("Gemini processing completed",
		"total_files", len(files),
		"successful_processing", successCount,
		"failed_processing", errorCount,
		"total_offers_extracted", len(offers))

	return offers
}

func parseResponseJson(resp *genai.GenerateContentResponse) []Offer {
	logger := log.With("operation", "parse_gemini_response")
	return parseResponseJsonWithLogger(resp, logger)
}

func parseResponseJsonWithLogger(resp *genai.GenerateContentResponse, logger *slog.Logger) []Offer {
	if resp == nil {
		logger.Error("Received nil response from Gemini")
		return []Offer{}
	}

	logger.Debug("Parsing Gemini response", "candidates_count", len(resp.Candidates))

	for candidateIndex, candidate := range resp.Candidates {
		logger.Debug("Processing candidate", "index", candidateIndex, "parts_count", len(candidate.Content.Parts))

		for partIndex, part := range candidate.Content.Parts {
			var offers []Offer

			rawJson, ok := part.(genai.Text)
			if !ok {
				logger.Warn("Unexpected part type in Gemini response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"part_type", fmt.Sprintf("%T", part))
				continue
			}

			logger.Debug("Attempting to parse JSON response",
				"candidate_index", candidateIndex,
				"part_index", partIndex,
				"json_length", len(rawJson))

			if err := json.Unmarshal([]byte(rawJson), &offers); err != nil {
				logger.Error("Failed to unmarshal Gemini JSON response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"error", err,
					"raw_json", string(rawJson))
				continue
			}

			if len(offers) > 0 {
				logger.Info("Successfully parsed offers from Gemini response",
					"offers_count", len(offers))
				for i, offer := range offers {
					logger.Debug("Parsed offer",
						"index", i,
						"product", offer.ProductName,
						"price", offer.Price,
						"currency", offer.Currency,
						"size", offer.Size)
				}
			} else {
				logger.Warn("Gemini response contained no offers")
			}

			return offers
		}
	}

	logger.Warn("No valid offers found in Gemini response")
	return []Offer{}
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

		"successful_processing", successCount,
		"failed_processing", errorCount,
		"total_offers_extracted", len(offers))

	return offers
}

func parseResponseJson(resp *genai.GenerateContentResponse) []Offer {
	logger := log.With("operation", "parse_gemini_response")
	return parseResponseJsonWithLogger(resp, logger)
}

func parseResponseJsonWithLogger(resp *genai.GenerateContentResponse, logger *slog.Logger) []Offer {
	if resp == nil {
		logger.Error("Received nil response from Gemini")
		return []Offer{}
	}

	logger.Debug("Parsing Gemini response", "candidates_count", len(resp.Candidates))

	for candidateIndex, candidate := range resp.Candidates {
		logger.Debug("Processing candidate", "index", candidateIndex, "parts_count", len(candidate.Content.Parts))

		for partIndex, part := range candidate.Content.Parts {
			var offers []Offer

			rawJson, ok := part.(genai.Text)
			if !ok {
				logger.Warn("Unexpected part type in Gemini response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"part_type", fmt.Sprintf("%T", part))
				continue
			}

			logger.Debug("Attempting to parse JSON response",
				"candidate_index", candidateIndex,
				"part_index", partIndex,
				"json_length", len(rawJson))

			if err := json.Unmarshal([]byte(rawJson), &offers); err != nil {
				logger.Error("Failed to unmarshal Gemini JSON response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"error", err,
					"raw_json", string(rawJson))
				continue
			}

			if len(offers) > 0 {
				logger.Info("Successfully parsed offers from Gemini response",
					"offers_count", len(offers))
				for i, offer := range offers {
					logger.Debug("Parsed offer",
						"index", i,
						"product", offer.ProductName,
						"price", offer.Price,
						"currency", offer.Currency,
						"size", offer.Size)
				}
			} else {
				logger.Warn("Gemini response contained no offers")
			}

			return offers
		}
	}

	logger.Warn("No valid offers found in Gemini response")
	return []Offer{}
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

	logger.Info("Gemini processing completed",
		"total_files", len(files),
		"successful_processing", successCount,
		"failed_processing", errorCount,
		"total_offers_extracted", len(offers))

	return offers
}

func parseResponseJson(resp *genai.GenerateContentResponse) []Offer {
	logger := log.With("operation", "parse_gemini_response")
	return parseResponseJsonWithLogger(resp, logger)
}

func parseResponseJsonWithLogger(resp *genai.GenerateContentResponse, logger *slog.Logger) []Offer {
	if resp == nil {
		logger.Error("Received nil response from Gemini")
		return []Offer{}
	}

	logger.Debug("Parsing Gemini response", "candidates_count", len(resp.Candidates))

	for candidateIndex, candidate := range resp.Candidates {
		logger.Debug("Processing candidate", "index", candidateIndex, "parts_count", len(candidate.Content.Parts))

		for partIndex, part := range candidate.Content.Parts {
			var offers []Offer

			rawJson, ok := part.(genai.Text)
			if !ok {
				logger.Warn("Unexpected part type in Gemini response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"part_type", fmt.Sprintf("%T", part))
				continue
			}

			logger.Debug("Attempting to parse JSON response",
				"candidate_index", candidateIndex,
				"part_index", partIndex,
				"json_length", len(rawJson))

			if err := json.Unmarshal([]byte(rawJson), &offers); err != nil {
				logger.Error("Failed to unmarshal Gemini JSON response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"error", err,
					"raw_json", string(rawJson))
				continue
			}

			if len(offers) > 0 {
				logger.Info("Successfully parsed offers from Gemini response",
					"offers_count", len(offers))
				for i, offer := range offers {
					logger.Debug("Parsed offer",
						"index", i,
						"product", offer.ProductName,
						"price", offer.Price,
						"currency", offer.Currency,
						"size", offer.Size)
				}
			} else {
				logger.Warn("Gemini response contained no offers")
			}

			return offers
		}
	}

	logger.Warn("No valid offers found in Gemini response")
	return []Offer{}
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

	logger.Info("Gemini processing completed",
		"total_files", len(files),
		"successful_processing", successCount,
		"failed_processing", errorCount,
		"total_offers_extracted", len(offers))

	return offers
}

func parseResponseJson(resp *genai.GenerateContentResponse) []Offer {
	logger := log.With("operation", "parse_gemini_response")
	return parseResponseJsonWithLogger(resp, logger)
}

func parseResponseJsonWithLogger(resp *genai.GenerateContentResponse, logger *slog.Logger) []Offer {
	if resp == nil {
		logger.Error("Received nil response from Gemini")
		return []Offer{}
	}

	logger.Debug("Parsing Gemini response", "candidates_count", len(resp.Candidates))

	for candidateIndex, candidate := range resp.Candidates {
		logger.Debug("Processing candidate", "index", candidateIndex, "parts_count", len(candidate.Content.Parts))

		for partIndex, part := range candidate.Content.Parts {
			var offers []Offer

			rawJson, ok := part.(genai.Text)
			if !ok {
				logger.Warn("Unexpected part type in Gemini response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"part_type", fmt.Sprintf("%T", part))
				continue
			}

			logger.Debug("Attempting to parse JSON response",
				"candidate_index", candidateIndex,
				"part_index", partIndex,
				"json_length", len(rawJson))

			if err := json.Unmarshal([]byte(rawJson), &offers); err != nil {
				logger.Error("Failed to unmarshal Gemini JSON response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"error", err,
					"raw_json", string(rawJson))
				continue
			}

			if len(offers) > 0 {
				logger.Info("Successfully parsed offers from Gemini response",
					"offers_count", len(offers))
				for i, offer := range offers {
					logger.Debug("Parsed offer",
						"index", i,
						"product", offer.ProductName,
						"price", offer.Price,
						"currency", offer.Currency,
						"size", offer.Size)
				}
			} else {
				logger.Warn("Gemini response contained no offers")
			}

			return offers
		}
	}

	logger.Warn("No valid offers found in Gemini response")
	return []Offer{}
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


		"failed_processing", errorCount,
		"total_offers_extracted", len(offers))

	return offers
}

func parseResponseJson(resp *genai.GenerateContentResponse) []Offer {
	logger := log.With("operation", "parse_gemini_response")
	return parseResponseJsonWithLogger(resp, logger)
}

func parseResponseJsonWithLogger(resp *genai.GenerateContentResponse, logger *slog.Logger) []Offer {
	if resp == nil {
		logger.Error("Received nil response from Gemini")
		return []Offer{}
	}

	logger.Debug("Parsing Gemini response", "candidates_count", len(resp.Candidates))

	for candidateIndex, candidate := range resp.Candidates {
		logger.Debug("Processing candidate", "index", candidateIndex, "parts_count", len(candidate.Content.Parts))

		for partIndex, part := range candidate.Content.Parts {
			var offers []Offer

			rawJson, ok := part.(genai.Text)
			if !ok {
				logger.Warn("Unexpected part type in Gemini response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"part_type", fmt.Sprintf("%T", part))
				continue
			}

			logger.Debug("Attempting to parse JSON response",
				"candidate_index", candidateIndex,
				"part_index", partIndex,
				"json_length", len(rawJson))

			if err := json.Unmarshal([]byte(rawJson), &offers); err != nil {
				logger.Error("Failed to unmarshal Gemini JSON response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"error", err,
					"raw_json", string(rawJson))
				continue
			}

			if len(offers) > 0 {
				logger.Info("Successfully parsed offers from Gemini response",
					"offers_count", len(offers))
				for i, offer := range offers {
					logger.Debug("Parsed offer",
						"index", i,
						"product", offer.ProductName,
						"price", offer.Price,
						"currency", offer.Currency,
						"size", offer.Size)
				}
			} else {
				logger.Warn("Gemini response contained no offers")
			}

			return offers
		}
	}

	logger.Warn("No valid offers found in Gemini response")
	return []Offer{}
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

	logger.Info("Gemini processing completed",
		"total_files", len(files),
		"successful_processing", successCount,
		"failed_processing", errorCount,
		"total_offers_extracted", len(offers))

	return offers
}

func parseResponseJson(resp *genai.GenerateContentResponse) []Offer {
	logger := log.With("operation", "parse_gemini_response")
	return parseResponseJsonWithLogger(resp, logger)
}

func parseResponseJsonWithLogger(resp *genai.GenerateContentResponse, logger *slog.Logger) []Offer {
	if resp == nil {
		logger.Error("Received nil response from Gemini")
		return []Offer{}
	}

	logger.Debug("Parsing Gemini response", "candidates_count", len(resp.Candidates))

	for candidateIndex, candidate := range resp.Candidates {
		logger.Debug("Processing candidate", "index", candidateIndex, "parts_count", len(candidate.Content.Parts))

		for partIndex, part := range candidate.Content.Parts {
			var offers []Offer

			rawJson, ok := part.(genai.Text)
			if !ok {
				logger.Warn("Unexpected part type in Gemini response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"part_type", fmt.Sprintf("%T", part))
				continue
			}

			logger.Debug("Attempting to parse JSON response",
				"candidate_index", candidateIndex,
				"part_index", partIndex,
				"json_length", len(rawJson))

			if err := json.Unmarshal([]byte(rawJson), &offers); err != nil {
				logger.Error("Failed to unmarshal Gemini JSON response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"error", err,
					"raw_json", string(rawJson))
				continue
			}

			if len(offers) > 0 {
				logger.Info("Successfully parsed offers from Gemini response",
					"offers_count", len(offers))
				for i, offer := range offers {
					logger.Debug("Parsed offer",
						"index", i,
						"product", offer.ProductName,
						"price", offer.Price,
						"currency", offer.Currency,
						"size", offer.Size)
				}
			} else {
				logger.Warn("Gemini response contained no offers")
			}

			return offers
		}
	}

	logger.Warn("No valid offers found in Gemini response")
	return []Offer{}
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

	logger.Info("Gemini processing completed",
		"total_files", len(files),
		"successful_processing", successCount,
		"failed_processing", errorCount,
		"total_offers_extracted", len(offers))

	return offers
}

func parseResponseJson(resp *genai.GenerateContentResponse) []Offer {
	logger := log.With("operation", "parse_gemini_response")
	return parseResponseJsonWithLogger(resp, logger)
}

func parseResponseJsonWithLogger(resp *genai.GenerateContentResponse, logger *slog.Logger) []Offer {
	if resp == nil {
		logger.Error("Received nil response from Gemini")
		return []Offer{}
	}

	logger.Debug("Parsing Gemini response", "candidates_count", len(resp.Candidates))

	for candidateIndex, candidate := range resp.Candidates {
		logger.Debug("Processing candidate", "index", candidateIndex, "parts_count", len(candidate.Content.Parts))

		for partIndex, part := range candidate.Content.Parts {
			var offers []Offer

			rawJson, ok := part.(genai.Text)
			if !ok {
				logger.Warn("Unexpected part type in Gemini response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"part_type", fmt.Sprintf("%T", part))
				continue
			}

			logger.Debug("Attempting to parse JSON response",
				"candidate_index", candidateIndex,
				"part_index", partIndex,
				"json_length", len(rawJson))

			if err := json.Unmarshal([]byte(rawJson), &offers); err != nil {
				logger.Error("Failed to unmarshal Gemini JSON response",
					"candidate_index", candidateIndex,
					"part_index", partIndex,
					"error", err,
					"raw_json", string(rawJson))
				continue
			}

			if len(offers) > 0 {
				logger.Info("Successfully parsed offers from Gemini response",
					"offers_count", len(offers))
				for i, offer := range offers {
					logger.Debug("Parsed offer",
						"index", i,
						"product", offer.ProductName,
						"price", offer.Price,
						"currency", offer.Currency,
						"size", offer.Size)
				}
			} else {
				logger.Warn("Gemini response contained no offers")
			}

			return offers
		}
	}

	logger.Warn("No valid offers found in Gemini response")
	return []Offer{}
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


}


