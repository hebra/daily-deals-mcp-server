# Ask Mode Rules (Non-Obvious Only)

## Project Structure
- MCP server implementation using `github.com/ThinkInAIXYZ/go-mcp` library
- SSE (Server-Sent Events) transport on `/sse` endpoint, POST messages on `/message`
- Single tool registered: `get-big-watermelon-deals` with empty parameters

## Data Flow
- Web scraping → Image download → GCP upload → Gemini analysis → JSON response
- Images scraped from `https://www.bigwatermelon.com.au/category/specials/`
- Regex pattern specifically looks for "SPECIALS" or "SPECIAL" in image URLs (case-insensitive)

## API Constraints
- Gemini model: `gemini-2.0-flash` with JSON response mode
- Prompt instructs Gemini about vertical/horizontal black lines separating offers
- Product names normalized to title case by Gemini prompt

## Caching Strategy
- Single cache file `bigwatermelon-dailydeals.cached.json` in project root
- No cache expiration beyond date check - manual deletion required for force refresh
- Cache checked synchronously before any network operations
