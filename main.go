package main

import (
	"context"
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
	config := internal.LoadConfig()

	bytes, err := json.Marshal(internal.FetchBigWatermelonDailyDeals(config))
	if err != nil {
		log.Error("Error marshalling JSON.", "Error", err)
		return nil, err
	}

	return &protocol.CallToolResult{
		Content: []protocol.Content{
			&protocol.TextContent{
				Type: "text",
				Text: string(bytes),
			},
		},
	}, nil

}
