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

func FetchBigWatermelonDailyDeals(config *Config) ResponseData {
	ctx := context.Background()

	localResp := checkLocalFile(config)

	if localResp.LastUpdated == time.Now().Format(dateFormat) {
		log.Info("Local file is up to date.")
		return localResp
	}

	localTime, err := time.LoadLocation(config.Timezone)
	if err != nil {
		log.Error("Error loading location.", "Error", err)
		return ResponseData{}
	}

	// Wait until configured hour before fetching deals of the day
	if time.Now().In(localTime).Hour() < config.FetchHour {
		log.Info("Not fetching deals of the day yet.", "required_hour", config.FetchHour)
		return ResponseData{
			LastUpdated: time.Now().Format(dateFormat),
			Business:    config.BusinessName,
			Location:    config.Location,
		}
	}

	var client = getClient(ctx)
	defer func(client *genai.Client) {
		err := client.Close()
		if err != nil {
			log.Error("Error closing client", "Error", err)
		}
	}(client)

	cleanUpGcpFiles(ctx, client, config)

	images := downloadImagesFromBigWatermelon(config)
	gcpFiles := uploadImagesToGoogleCloud(ctx, client, images, config)

	resp := ResponseData{
		LastUpdated: time.Now().Format(dateFormat),
		Business:    config.BusinessName,
		Location:    config.Location,
		Offers:      makeRequestToGemini(ctx, client, gcpFiles, config),
	}

	writeOffersToFile(resp, config)

	return resp
}

func checkLocalFile(config *Config) ResponseData {
	if _, err := os.Stat(config.CacheFile); errors.Is(err, os.ErrNotExist) {
		log.Info("No local file found.")
		return ResponseData{}
	}

	content, err := os.ReadFile(config.CacheFile)
	if err != nil {
		log.Error("Error reading local file.", "Error", err)
		return ResponseData{}
	}

	var resp ResponseData
	if err := json.Unmarshal(content, &resp); err != nil {
		log.Error("Error unmarshalling JSON.", "Error", err)
		return ResponseData{}
	}

	return resp
}

func getClient(ctx context.Context) *genai.Client {
	client, err := genai.NewClient(ctx,
		option.WithAPIKey(os.Getenv("GEMINI_API_KEY")))
	if err != nil {
		log.Error("Error creating Gemini client.", "Error", err)
	}
	return client
}

func cleanUpGcpFiles(ctx context.Context, client *genai.Client, config *Config) {
	files := client.ListFiles(ctx)

	for {
		file, err := files.Next()
		if err != nil {
			if errors.Is(err, iterator.Done) {
				break
			}
			log.Error("Error while listing files:", "Error", err)
			return
		}

		if strings.Contains(file.Name, config.GCPFilePrefix) {
			log.Info("Deleting file.", "Name", file.Name)
			err = client.DeleteFile(ctx, file.Name)
			if err != nil {
				log.Error("Error deleting file", "name", file.Name, "Error", err)
			}
		}
	}
}

func downloadImagesFromBigWatermelon(config *Config) [][]byte {
	var imageList [][]byte
	var mu sync.Mutex

	url := config.SpecialsURL

	log.Info("Downloading images.", "URL", url)
	resp, err := http.Get(url)
	if err != nil {
		log.Error("Error fetching the URL.", "Error", err)
		return imageList
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Error("Error closing response body.", "Error", err)
		}
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		log.Error("Failed to fetch URL", "URL", url, "status code", resp.StatusCode)
		return imageList
	}
	log.Info("Successfully fetched content.")

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error("Error reading the response body.", "Error", err)
		return imageList
	}

	htmlContent := string(body)

	// Example Special URL from BigWatermelon
	// https://www.bigwatermelon.com.au/wp-content/uploads/2025/04/1-2.FRI-SPECIALS-11-4-25.jpg
	// https://www.bigwatermelon.com.au/wp-content/uploads/2025/05/1-2.SPECIAL-TUE-27-5-25.jpg

	regex := regexp.MustCompile(`(?i)href="([^"]*SPECIALS?[^"]*\.jpg)"`)

	matches := regex.FindAllStringSubmatch(htmlContent, -1)

	log.Info("Extracted SPECIALS image URLs.")

	for _, match := range matches {
		log.Info("Extracted SPECIALS image URL.", "URL", match[1])
	}

	if matches != nil {
		var wg sync.WaitGroup
		wg.Add(len(matches))

		for _, match := range matches {
			log.Info("Downloading image.", "URL", match[1])

			go func() {
				image, err := http.Get(match[1])

				if err != nil {
					log.Error("Error fetching specials image from URL.", "Error", err)
					wg.Done()
					return
				}

				defer func(Body io.ReadCloser) {
					err := Body.Close()
					if err != nil {
						log.Error("Error closing response body.", "Error", err)
					}
				}(image.Body)

				if image.StatusCode != http.StatusOK {
					log.Error("Failed to fetch specials image.", "URL", match[1], "status code", resp.StatusCode)
					wg.Done()
					return
				}

				imageData, err := io.ReadAll(image.Body)
				if err != nil {
					log.Error("Error reading the response body:", "Error", err)
				}

				mu.Lock()
				imageList = append(imageList, imageData)
				mu.Unlock()

				wg.Done()
			}()
		}
		wg.Wait()
	} else {
		log.Error("No SPECIAL-OFFERS images found in the HTML content.")
		return imageList
	}

	return imageList
}

func uploadImagesToGoogleCloud(ctx context.Context, client *genai.Client, images [][]byte, config *Config) []*genai.File {

	var files []*genai.File
	var mu sync.Mutex

	var wg sync.WaitGroup
	wg.Add(len(images))

	for imageIndex, image := range images {
		go func() {
			if len(image) == 0 {
				log.Error("Empty image.", "Index", imageIndex)
				wg.Done()
				return
			}
			reader := bytes.NewReader(image)

			imageName := config.GCPFilePrefix + fmt.Sprint(imageIndex) + "-jpg"

			log.Info("Uploading image.", "Index", imageIndex, "Name", imageName)

			options := genai.UploadFileOptions{
				DisplayName: imageName,
				MIMEType:    "image/jpeg",
			}

			file, err := client.UploadFile(ctx, imageName, reader, &options)
			if err != nil {
				log.Error("Failed to upload image to Gemini", "Error", err)
			}

			log.Info("Uploading image successful.", "Index", imageIndex)

			mu.Lock()
			files = append(files, file)
			mu.Unlock()

			wg.Done()
		}()

	}

	wg.Wait()
	return files
}

func makeRequestToGemini(ctx context.Context, client *genai.Client, files []*genai.File, config *Config) []Offer {

	var offers []Offer
	var mu sync.Mutex

	var wg sync.WaitGroup
	wg.Add(len(files))

	log.Info("Querying Gemini to extract data from images.")

	for _, file := range files {
		go func() {
			defer func(client *genai.Client, ctx context.Context, name string) {
				err := client.DeleteFile(ctx, name)
				if err != nil {
					log.Error("Error deleting file", "Error", err)
				}
			}(client, ctx, file.Name)

			log.Info("Requesting Gemini to extract data from image.", "Name", file.Name)

			genmodels := client.GenerativeModel(config.GeminiModel)
			genmodels.ResponseMIMEType = "application/json"
			resp, err := genmodels.GenerateContent(ctx,
				genai.FileData{URI: file.URI},
				genai.Text(`
The image is an advertisement for fruits and vegetables that are on sale.
Offers are separated by thing vertical and horizontal black lines.
There are one, two or three offer columns per row.
The name and price of the fruits are in the right lower corner of each row.
Please extract the name and price of each offer from the image.
Split each item into product name, price, currency and optionally the packaging type (e.g. ea, pk, kg etc.).
Normalize the product names to start with upper case letters and the rest lower case letters.
For the result use this JSON schema:
Offer = {'productName': string, 'price': number, 'currency': string, 'size': string}
Return: Array<Offer>
`))
			if err != nil {
				log.Error("Error ", "Error", err)
				wg.Done()
				return
			}

			log.Info("Data extraction successful for image.", "Name", file.Name)
			mu.Lock()
			offers = append(offers, parseResponseJson(resp)...)
			mu.Unlock()
			wg.Done()
		}()
	}

	wg.Wait()

	return offers
}

func parseResponseJson(resp *genai.GenerateContentResponse) []Offer {
	if resp == nil {
		log.Error("Empty response received.")
		return []Offer{}
	}

	for _, candidate := range resp.Candidates {
		for _, part := range candidate.Content.Parts {

			var offers []Offer

			if rawJson, ok := part.(genai.Text); ok {
				if err := json.Unmarshal([]byte(rawJson), &offers); err != nil {
					log.Error("Error unmarshalling JSON", "Error", err)
				}
			}

			log.Info("Offers extracted", "Offers", offers)

			return offers
		}
	}
	return []Offer{}
}

func writeOffersToFile(resp ResponseData, config *Config) {
	jsonData, err := json.MarshalIndent(resp, "", "\t")
	if err != nil {
		log.Error("Error transforming into JSON.", "Error", err)
	}

	err = os.WriteFile(config.CacheFile, jsonData, 0644)
	if err == nil {
		log.Info("Wrote file", "File", config.CacheFile)
	} else {
		log.Error("Error writing file.", "Error", err)
	}
}
