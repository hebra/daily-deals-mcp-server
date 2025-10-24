package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"github.com/ThinkInAIXYZ/go-mcp/protocol"
	"github.com/ThinkInAIXYZ/go-mcp/server"
	"github.com/ThinkInAIXYZ/go-mcp/transport"
	"github.com/gin-gonic/gin"
	"github.com/hebra/ahemseepee/daily-deals-mcp-server/internal"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

var log = slog.New(slog.NewTextHandler(os.Stderr, nil))

type DealsRequest struct {
}

// RateLimiter implements a simple token bucket rate limiter
type RateLimiter struct {
	tokens    int
	maxTokens int
	window    time.Duration
	lastRefill time.Time
	mu        sync.Mutex
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(requests int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		tokens:     requests,
		maxTokens:  requests,
		window:     window,
		lastRefill: time.Now(),
	}
}

// Allow checks if a request should be allowed
func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastRefill)

	// Refill tokens based on elapsed time
	if elapsed >= rl.window {
		rl.tokens = rl.maxTokens
		rl.lastRefill = now
	}

	if rl.tokens > 0 {
		rl.tokens--
		return true
	}

	return false
}

// RateLimitMiddleware creates a Gin middleware for rate limiting
func RateLimitMiddleware(limiter *RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !limiter.Allow() {
			c.Header("X-RateLimit-Limit", "10")
			c.Header("X-RateLimit-Remaining", "0")
			c.Header("Retry-After", "60")
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": "Rate limit exceeded",
				"message": "Too many requests. Please try again later.",
			})
			c.Abort()
			return
		}

		// Add rate limit headers
		c.Header("X-RateLimit-Limit", "10")
		c.Header("X-RateLimit-Remaining", "9") // Simplified, would need proper tracking
		c.Next()
	}
}

// generateRequestID creates a unique request ID for tracing
func generateRequestID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func main() {
	log.Info("Starting offers extractor...")

	config := internal.LoadConfig()

	messageEndpointURL := "/message"

	sseTransport, mcpHandler, err := transport.NewSSEServerTransportAndHandler(messageEndpointURL)
	if err != nil {
		log.Error("Error creating SEE transport and handler.", "Error", err)
		os.Exit(1)
	}

	mcpServer, err := server.NewServer(sseTransport)
	if err != nil {
		log.Error("Failed to create MCP server.", "Error", err)
		os.Exit(1)
	}

	tool, err := protocol.NewTool("get-big-watermelon-deals",
		"Get today's deals from Big Watermelon",
		DealsRequest{})

	if err != nil {
		log.Error("Failed to create tool.", "Error", err)
		os.Exit(1)
	}
	mcpServer.RegisterTool(tool, getDailyDealsHandler)

	go func() {
		err := mcpServer.Run()
		if err != nil {
			log.Error("Failed to start MCP server.", "Error", err)
			os.Exit(1)
		}
	}()

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Info("Received shutdown signal, shutting down gracefully...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := mcpServer.Shutdown(ctx)
		if err != nil {
			log.Error("Failed to shutdown MCP server.", "Error", err)
			os.Exit(1)
		}
		log.Info("Server shutdown complete")
		os.Exit(0)
	}()

	r := gin.Default()

	// Create rate limiter
	rateLimiter := NewRateLimiter(config.RateLimitRequests, config.RateLimitWindow)

	// Health check endpoints (no rate limiting)
	r.GET("/health", func(ctx *gin.Context) {
		ctx.JSON(200, gin.H{
			"status": "healthy",
			"time":   time.Now().Format(time.RFC3339),
		})
	})
	r.GET("/ready", func(ctx *gin.Context) {
		ctx.JSON(200, gin.H{
			"status": "ready",
			"time":   time.Now().Format(time.RFC3339),
		})
	})

	// Apply rate limiting to MCP endpoints
	r.GET("/sse", RateLimitMiddleware(rateLimiter), func(ctx *gin.Context) {
		mcpHandler.HandleSSE().ServeHTTP(ctx.Writer, ctx.Request)
	})
	r.POST(messageEndpointURL, RateLimitMiddleware(rateLimiter), func(ctx *gin.Context) {
		mcpHandler.HandleMessage().ServeHTTP(ctx.Writer, ctx.Request)
	})

	if err = r.Run(":" + config.Port); err != nil {
		log.Error("Failed to start HTTP server.", "Error", err)
		os.Exit(1)
	}
}

func getDailyDealsHandler(ctx context.Context, req *protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	requestID := generateRequestID()
	logger := log.With("request_id", requestID, "tool", req.Name)

	logger.Info("Processing get-big-watermelon-deals request")

	config := internal.LoadConfig()
	logger.Debug("Configuration loaded", "business", config.BusinessName, "cache_file", config.CacheFile)

	start := time.Now()
	result := internal.FetchBigWatermelonDailyDeals(config)
	duration := time.Since(start)

	logger.Info("Deals fetch completed",
		"duration_ms", duration.Milliseconds(),
		"offers_count", len(result.Offers),
		"last_updated", result.LastUpdated)

	bytes, err := json.Marshal(result)
	if err != nil {
		logger.Error("Failed to marshal response JSON", "error", err)
		return nil, err
	}

	logger.Debug("Response prepared", "response_size_bytes", len(bytes))

	return &protocol.CallToolResult{
		Content: []protocol.Content{
			&protocol.TextContent{
				Type: "text",
				Text: string(bytes),
			},
		},
	}, nil
}
