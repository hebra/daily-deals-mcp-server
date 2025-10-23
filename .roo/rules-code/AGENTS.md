# Code Mode Rules (Non-Obvious Only)

## Concurrency Gotchas
- Slice appends in goroutines lack mutex protection (lines 225, 271, 325 in [`internal/big-watermelon-deals-fetcher.go`](../../internal/big-watermelon-deals-fetcher.go:225)) - potential race condition
- All concurrent operations use `sync.WaitGroup` but no synchronization on shared data structures

## API Integration
- Gemini client MUST be closed with defer (line 68-73)
- GCP file cleanup happens BEFORE new uploads to avoid quota issues
- File deletion is deferred within goroutines (line 293-297) - happens after processing

## Time Handling
- Uses `time/tzdata` import for embedded timezone data (line 20)
- Australia/Melbourne timezone loaded explicitly for 7 AM check (line 51-55)
