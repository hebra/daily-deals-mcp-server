# AGENTS.md

This file provides guidance to agents when working with code in this repository.

## Non-Obvious Project Details

### Image Processing Regex
- Images scraped using case-insensitive pattern `(?i)href="([^"]*SPECIALS?[^"]*\.jpg)"` - matches both "SPECIAL" and "SPECIALS" in URLs

### Time Handling
- Uses `time/tzdata` import for embedded timezone data (required for deployment environments without system timezone files)
- Server waits until configured hour (default 7 AM) in Australia/Melbourne timezone before fetching deals

### Requesty.ai Integration
- Uses OpenAI-compatible API format with base64-encoded images in data URLs
- Images sent as `data:image/jpeg;base64,...` format in message content array
- Prompt includes cache control with `"type": "ephemeral"` for text content
- Response format MUST be `"type": "json_object"` for structured output
- Product names normalized to title case in prompt
- Prompt explicitly mentions "one, two or three offer columns per row" - critical for correct parsing

### API Configuration
- Requires `REQUESTY_API_KEY` environment variable
- Config validation happens at startup - exits with error if required fields missing
- HTTP client configured with connection pooling (100 max idle, 10 per host)

### Testing
- Run single test: `go test -v ./internal -run TestFunctionName`
- Coverage: `make coverage` opens HTML report in browser
