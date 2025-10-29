# Architect Mode Rules (Non-Obvious Only)

## Architecture Constraints
- MCP server runs on port 8080 with Gin framework
- SSE transport requires two endpoints: GET `/sse` for connection, POST `/message` for requests
- Server starts MCP in goroutine, HTTP server blocks main thread

## Concurrency Design
- Three parallel processing stages: image download, Requesty.ai analysis
- No synchronization between goroutines appending to shared slices - race condition exists
- `sync.WaitGroup` used for coordination but not for data protection

## External Dependencies
- Requesty.ai API (requires `REQUESTY_API_KEY`)
- Web scraping target: `bigwatermelon.com.au` (HTML parsing, not API)

## State Management
- Stateless except for file-based cache (`bigwatermelon-dailydeals.cached.json`)
- No database, no persistent storage beyond cache file
- Time-based gating at 7 AM Australia/Melbourne prevents early fetches

## Scalability Considerations
- Single-instance design (no distributed coordination)
- GCP file cleanup before upload prevents quota accumulation
- Concurrent Requesty.ai requests limited by number of images found (typically 1-3)
