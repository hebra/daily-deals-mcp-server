# Code Mode Rules (Non-Obvious Only)

## API Integration
- Requesty.ai uses OpenAI-compatible format - images MUST be base64-encoded data URLs
- Response format MUST be `"type": "json_object"` (not `application/json` MIME type)
- Cache control with `"type": "ephemeral"` required on text content for prompt caching
- API requires `REQUESTY_API_KEY` environment variable

## Time Handling
- Uses `time/tzdata` import for embedded timezone data (line 15) - required for environments without system timezone files
- Australia/Melbourne timezone loaded explicitly for fetch hour check
- Date format constant `dateFormat = "2006-01-02"` used throughout (Go's reference time format)

## Concurrency Patterns
- All concurrent operations use mutex-protected slice appends (lines 252, 298, 460)
- Worker pool pattern NOT implemented - uses unbounded goroutines with WaitGroup
- Each goroutine handles its own error counting with mutex protection
- Image downloads happen concurrently but results collected in single slice

## Configuration
- All functions accept `*Config` parameter - no global config state
- Logger instances created with context using `log.With()` for structured logging
- HTTP client with timeout is part of Config struct, not created per-request
- Config validation runs at startup - app exits immediately if validation fails

## Error Handling
- Retry logic uses exponential backoff with `retryWithBackoff()` helper
- Context cancellation checked during retry delays (select with ctx.Done())
- HTTP response bodies MUST be closed with defer - even in error paths
