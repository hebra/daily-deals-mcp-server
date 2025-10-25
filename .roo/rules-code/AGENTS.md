# Code Mode Rules (Non-Obvious Only)

## API Integration
- Gemini client MUST be closed with defer after creation
- GCP file cleanup happens BEFORE new uploads to avoid quota issues
- File deletion is deferred within goroutines - happens after Gemini processing completes

## Time Handling
- Uses `time/tzdata` import for embedded timezone data (line 21) - required for environments without system timezone files
- Australia/Melbourne timezone loaded explicitly for fetch hour check

## Concurrency Patterns
- All concurrent operations use mutex-protected slice appends (lines 252, 382, 460)
- Worker pool pattern NOT implemented - uses unbounded goroutines with WaitGroup
- Each goroutine handles its own error counting with mutex protection

## Configuration
- All functions accept `*Config` parameter - no global config state
- Logger instances created with context using `log.With()` for structured logging
- HTTP client with timeout is part of Config struct, not created per-request
