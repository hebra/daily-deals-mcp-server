# Debug Mode Rules (Non-Obvious Only)

## Logging Patterns
- All logging uses `slog.New(slog.NewTextHandler(os.Stderr, nil))` - logs go to stderr, not stdout
- Separate logger instances in [`main.go`](../../main.go:15) and [`internal/big-watermelon-deals-fetcher.go`](../../internal/big-watermelon-deals-fetcher.go:29)

## Error Handling
- HTTP response bodies must be closed with defer to prevent leaks (lines 156-161, 207-212)
- Requesty.ai client must be closed with defer (line 68-73)
- Silent failures possible in goroutines without proper error propagation

## Cache Debugging
- Check `bigwatermelon-dailydeals.cached.json` for cached data
- Cache validation based on `LastUpdated` field matching current date format "2006-01-02"
- Empty cache file or missing `LastUpdated` triggers fresh fetch

## Time-Based Behavior
- Server behavior changes at 7 AM Australia/Melbourne time
- Before 7 AM: returns empty offers with current date
- After 7 AM: fetches and processes images
