package internal

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// RequestyMessage represents a message in the OpenAI-compatible format
type RequestyMessage struct {
	Role    string                   `json:"role"`
	Content []RequestyContentElement `json:"content"`
}

// CacheControl represents caching control for content
type CacheControl struct {
	Type string `json:"type"`
}

// RequestyContentElement represents a content element (text or image)
type RequestyContentElement struct {
	Type         string                `json:"type"`
	Text         string                `json:"text,omitempty"`
	ImageURL     *RequestyImageURL     `json:"image_url,omitempty"`
	CacheControl *CacheControl         `json:"cache_control,omitempty"`
}

// RequestyImageURL represents an image URL in the message
type RequestyImageURL struct {
	URL string `json:"url"`
}

// StreamOptions represents streaming options
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// RequestyRequest represents the request to requesty.ai
type RequestyRequest struct {
	Model          string            `json:"model"`
	Messages       []RequestyMessage `json:"messages"`
	ResponseFormat *ResponseFormat   `json:"response_format,omitempty"`
	MaxTokens      int               `json:"max_tokens,omitempty"`
	Temperature    float64           `json:"temperature,omitempty"`
	Stream         bool              `json:"stream,omitempty"`
	StreamOptions  *StreamOptions    `json:"stream_options,omitempty"`
	Requesty       *RequestyMetadata `json:"requesty,omitempty"`
}

// ResponseFormat specifies the response format
type ResponseFormat struct {
	Type string `json:"type"`
}

// RequestyMetadata represents Requesty-specific metadata
type RequestyMetadata struct {
	TraceID string                 `json:"trace_id,omitempty"`
	Extra   map[string]interface{} `json:"extra,omitempty"`
}

// RequestyError represents a structured error response from Requesty
type RequestyError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// parseRequestyError parses a structured error from Requesty API response
func parseRequestyError(statusCode int, body []byte) error {
	var reqErr RequestyError
	if err := json.Unmarshal(body, &reqErr); err == nil {
		return fmt.Errorf("requesty API error [%s]: %s", reqErr.Error.Type, reqErr.Error.Message)
	}
	return fmt.Errorf("requesty API returned status %d: %s", statusCode, string(body))
}

// RequestyResponse represents the response from requesty.ai
type RequestyResponse struct {
	ID      string           `json:"id"`
	Object  string           `json:"object"`
	Created int64            `json:"created"`
	Model   string           `json:"model"`
	Choices []RequestyChoice `json:"choices"`
	Usage   RequestyUsage    `json:"usage"`
}

// RequestyChoice represents a choice in the response
type RequestyChoice struct {
	Index        int                    `json:"index"`
	Message      RequestyResponseMessage `json:"message"`
	FinishReason string                 `json:"finish_reason"`
}

// RequestyResponseMessage represents the message in the response
type RequestyResponseMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// RequestyUsage represents token usage information
type RequestyUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// encodeImageToBase64 converts image bytes to base64 data URL
func encodeImageToBase64(imageData []byte) string {
	mediaType := http.DetectContentType(imageData)
	base64Data := base64.StdEncoding.EncodeToString(imageData)
	return fmt.Sprintf("data:%s;base64,%s", mediaType, base64Data)
}

// makeRequestyAPICall sends a request to requesty.ai and returns the response
func makeRequestyAPICall(ctx context.Context, config *Config, images [][]byte, logger *slog.Logger) ([]Offer, error) {
	if len(images) == 0 {
		logger.Warn("No images provided for Requesty API call")
		return []Offer{}, nil
	}

	// Build the content array with text prompt and images
	content := []RequestyContentElement{
		{
			Type: "text",
			Text: `The image is an advertisement for fruits and vegetables that are on sale.
Offers are separated by thing vertical and horizontal black lines.
There are one, two or three offer columns per row.
The name and price of the fruits are in the right lower corner of each row.
Please extract the name and price of each offer from the image.
Split each item into product name, price, currency and optionally the packaging type (e.g. ea, pk, kg etc.).
Normalize the product names to start with upper case letters and the rest lower case letters.
For the result use this JSON schema:
Offer = {'productName': string, 'price': number, 'currency': string, 'size': string}
Return: Array<Offer>`,
			CacheControl: &CacheControl{Type: "ephemeral"},
		},
	}

	// Add all images as base64-encoded data URLs
	for i, imageData := range images {
		logger.Debug("Encoding image to base64", "index", i, "size_bytes", len(imageData))
		base64URL := encodeImageToBase64(imageData)
		content = append(content, RequestyContentElement{
			Type: "image_url",
			ImageURL: &RequestyImageURL{
				URL: base64URL,
			},
		})
	}

	// Build the request
	request := RequestyRequest{
		Model: config.RequestyModel,
		Messages: []RequestyMessage{
			{
				Role:    "user",
				Content: content,
			},
		},
		ResponseFormat: &ResponseFormat{
			Type: "json_object",
		},
		MaxTokens:   config.RequestyMaxTokens,
		Temperature: config.RequestyTemperature,
		Requesty: &RequestyMetadata{
			TraceID: fmt.Sprintf("%d", time.Now().UnixNano()),
			Extra: map[string]interface{}{
				"mode": "batch-image-analysis",
			},
		},
	}

	// Marshal request to JSON
	requestBody, err := json.Marshal(request)
	if err != nil {
		logger.Error("Failed to marshal Requesty request", "error", err)
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	logger.Debug("Requesty request prepared",
		"model", config.RequestyModel,
		"images_count", len(images),
		"request_size_bytes", len(requestBody))

	// Create HTTP request
	apiURL := fmt.Sprintf("%s/chat/completions", config.RequestyBaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(requestBody))
	if err != nil {
		logger.Error("Failed to create HTTP request", "error", err)
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", config.RequestyAPIKey))

	logger.Info("Sending request to Requesty API", "url", apiURL, "model", config.RequestyModel)

	// Send request
	httpResp, err := config.HTTPClient.Do(httpReq)
	if err != nil {
		logger.Error("Requesty API request failed", "error", err)
		return nil, fmt.Errorf("requesty API request failed: %w", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			logger.Error("Error closing response body", "error", err)
		}
	}(httpResp.Body)

	// Read response body
	responseBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		logger.Error("Failed to read Requesty response body", "error", err)
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	logger.Debug("Requesty API response received",
		"status_code", httpResp.StatusCode,
		"response_size_bytes", len(responseBody))

	// Check for non-200 status
	if httpResp.StatusCode != http.StatusOK {
		logger.Error("Requesty API returned non-200 status",
			"status_code", httpResp.StatusCode,
			"response_body", string(responseBody))
		return nil, parseRequestyError(httpResp.StatusCode, responseBody)
	}

	// Parse response
	var requestyResp RequestyResponse
	if err := json.Unmarshal(responseBody, &requestyResp); err != nil {
		logger.Error("Failed to unmarshal Requesty response",
			"error", err,
			"response_body", string(responseBody))
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	logger.Info("Requesty API call successful",
		"model", requestyResp.Model,
		"choices_count", len(requestyResp.Choices),
		"prompt_tokens", requestyResp.Usage.PromptTokens,
		"completion_tokens", requestyResp.Usage.CompletionTokens,
		"total_tokens", requestyResp.Usage.TotalTokens)

	// Extract offers from response
	if len(requestyResp.Choices) == 0 {
		logger.Warn("Requesty response contained no choices")
		return []Offer{}, nil
	}

	// Parse the JSON content from the first choice
	contentStr := requestyResp.Choices[0].Message.Content
	logger.Debug("Parsing offers from response content", "content_length", len(contentStr))

	var offers []Offer
	if err := json.Unmarshal([]byte(contentStr), &offers); err != nil {
		logger.Error("Failed to unmarshal offers from response content",
			"error", err,
			"content", contentStr)
		return nil, fmt.Errorf("failed to unmarshal offers: %w", err)
	}

	logger.Info("Successfully extracted offers from Requesty response",
		"offers_count", len(offers))

	for i, offer := range offers {
		logger.Debug("Parsed offer",
			"index", i,
			"product", offer.ProductName,
			"price", offer.Price,
			"currency", offer.Currency,
			"size", offer.Size)
	}

	return offers, nil
}
