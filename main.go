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
	"os"
	"os/signal"
	"syscall"
	"time"
)

var log = slog.New(slog.NewTextHandler(os.Stderr, nil))

type DealsRequest struct {
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
	r.GET("/sse", func(ctx *gin.Context) {
		mcpHandler.HandleSSE().ServeHTTP(ctx.Writer, ctx.Request)
	})
	r.POST(messageEndpointURL, func(ctx *gin.Context) {
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
