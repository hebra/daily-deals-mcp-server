# Big Watermelon MCP Server

Big Watermelon is fruit & veggies wholesale store in the east of Melbourne.
The daily deals are announced on their [website](https://www.bigwatermelon.com.au/) 
each morning as a set of images.

This service will download the images ones per day, extract the deal details and makes
them available as a MCP server for AI agents to consume.

## Running

The service uses Google Gemini for image analysis and expects a valid Gemini API key
via the environment variable `GEMINI_API_KEY`, for example:

```shell
export GEMINI_API_KEY=ABCDE-XXXX-YYY-ETC
```

### Build and Run

```shell
make build
make run
```

The server will start on `http://localhost:8080` with the following endpoints:
- `GET /sse` - SSE connection endpoint
- `POST /message` - MCP message endpoint

### Development Workflow

```shell
make dev        # Start development server with hot reload
make fmt        # Format code
make lint       # Run linter
```

### Testing

```shell
make test
make coverage
```

### Docker Operations

```shell
make docker-build
make docker-run
```

## Configuring MCP Clients

This server uses **SSE (Server-Sent Events)** transport. You can either:
1. Run the server locally (see [Running](#running) section above)
2. Use the hosted instance at `https://daily-deals-mcp-server-7ow81.kinsta.app/`

### Using the Hosted Server

The hosted server is available at:
- **SSE endpoint**: `https://daily-deals-mcp-server-7ow81.kinsta.app/sse`
- **Message endpoint**: `https://daily-deals-mcp-server-7ow81.kinsta.app/message`

No setup required - just configure your MCP client to use the hosted URL.

### Running Locally

```shell
export GEMINI_API_KEY=your-gemini-api-key-here
make build
make run
```

The server will be available at `http://localhost:8080`.

### Cline / Roo Code

Add to your MCP settings file (typically `~/.roo/mcp_settings.json` or VSCode settings):

**Hosted Server:**
```json
{
  "mcpServers": {
    "bigwatermelon-deals": {
      "type": "sse",
      "url": "https://daily-deals-mcp-server-7ow81.kinsta.app/sse"
    }
  }
}
```

**Local Server:**
```json
{
  "mcpServers": {
    "bigwatermelon-deals": {
      "type": "sse",
      "url": "http://localhost:8080/sse"
    }
  }
}
```

### Claude Desktop

Add to `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows):

**Hosted Server:**
```json
{
  "mcpServers": {
    "bigwatermelon-deals": {
      "type": "sse",
      "url": "https://daily-deals-mcp-server-7ow81.kinsta.app/sse"
    }
  }
}
```

**Local Server:**
```json
{
  "mcpServers": {
    "bigwatermelon-deals": {
      "type": "sse",
      "url": "http://localhost:8080/sse"
    }
  }
}
```

### AnythingLLM

1. Navigate to Settings → MCP Servers
2. Add a new server with:
   - **Name**: `bigwatermelon-deals`
   - **Transport**: `SSE`
   - **URL**: `https://daily-deals-mcp-server-7ow81.kinsta.app/sse` (hosted) or `http://localhost:8080/sse` (local)

### Generic MCP Client Configuration

For any MCP-compatible client that supports SSE transport:

**Hosted Server:**
```json
{
  "transport": "sse",
  "url": "https://daily-deals-mcp-server-7ow81.kinsta.app/sse"
}
```

**Local Server:**
```json
{
  "transport": "sse",
  "url": "http://localhost:8080/sse"
}
```

**Important Notes:**
- **Hosted Server**: Ready to use immediately at `https://daily-deals-mcp-server-7ow81.kinsta.app/sse`
- **Local Server**: Must be running before connecting MCP clients (requires `GEMINI_API_KEY`)
- The server fetches deals once per day after 7 AM Australia/Melbourne time
- Deals are cached for 24 hours to minimize API calls

## Available Tools

### `get-big-watermelon-deals`

Retrieves today's fruit and vegetable deals from Big Watermelon.

**Parameters:** None

**Returns:**
```json
{
  "lastUpdated": "2025-01-23",
  "business": "Big Watermelon Bushy Park",
  "location": {
    "latitude": -37.8748714,
    "longitude": 145.2053244,
    "address": "1161 High St Rd",
    "city": "Wantirna South",
    "state": "VIC",
    "zip": "3152",
    "country": "Australia"
  },
  "offers": [
    {
      "productName": "Bananas",
      "price": 2.99,
      "currency": "AUD",
      "size": "kg"
    }
  ]
}
```
