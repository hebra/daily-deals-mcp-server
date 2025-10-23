# AGENTS.md

This file provides guidance to agents when working with code in this repository.

## Non-Obvious Project Details

### Environment Requirements
- `GEMINI_API_KEY` environment variable is REQUIRED (Google Gemini API for image analysis)
- Server waits until 7 AM Australia/Melbourne time before fetching deals (line 58 in [`internal/big-watermelon-deals-fetcher.go`](internal/big-watermelon-deals-fetcher.go:58))

### Caching Behavior
- Deals cached in `bigwatermelon-dailydeals.cached.json` with date-based validation
- Cache checked BEFORE fetching new data (line 34-39 in [`internal/big-watermelon-deals-fetcher.go`](internal/big-watermelon-deals-fetcher.go:34))
- Only fetches once per day based on `LastUpdated` field matching current date

### Image Processing
- Images scraped from HTML using regex pattern `(?i)href="([^"]*SPECIALS?[^"]*\.jpg)"` (line 181)
- GCP files prefixed with `au-bigwatermelon-image-` for cleanup tracking
- All uploaded images automatically deleted after Gemini processing (line 293-297)

### Concurrency
- Image downloads, uploads, and Gemini requests use goroutines with `sync.WaitGroup`
- No mutex protection on slice appends (lines 225, 271, 325) - potential race condition

### Testing
- Run single test: `go test -v ./internal -run TestFunctionName`
- Coverage: `make coverage` opens HTML report in browser
