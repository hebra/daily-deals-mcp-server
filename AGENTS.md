# AGENTS.md

This file provides guidance to agents when working with code in this repository.

## Non-Obvious Project Details

### Image Processing Regex
- Images scraped using case-insensitive pattern `(?i)href="([^"]*SPECIALS?[^"]*\.jpg)"` - matches both "SPECIAL" and "SPECIALS" in URLs

### Time Handling
- Uses `time/tzdata` import for embedded timezone data (required for deployment environments without system timezone files)
- Server waits until configured hour (default 7 AM) in Australia/Melbourne timezone before fetching deals

### GCP File Management
- GCP files prefixed with `au-bigwatermelon-image-` for cleanup tracking
- Cleanup happens BEFORE new uploads to avoid quota issues
- Files automatically deleted after Gemini processing via deferred cleanup in goroutines

### Gemini Prompt Structure
- Prompt explicitly mentions "one, two or three offer columns per row" - critical for correct parsing
- Response format must be `application/json` MIME type
- Product names normalized to title case in prompt

### Testing
- Run single test: `go test -v ./internal -run TestFunctionName`
- Coverage: `make coverage` opens HTML report in browser
