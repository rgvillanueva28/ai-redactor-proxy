# PII Redacting Proxy for AI CLIs

A high-performance HTTP proxy that sanitizes PII from AI CLI requests before forwarding to upstream providers (OpenAI, Anthropic, Gemini). The proxy redacts sensitive data using placeholder tags, forwards the sanitized request, then unredacts the response before returning to the client.

## Quick Start

### Build
```bash
go build -o redactor ./cmd/redactor
```

### Development
```bash
# Run with defaults (listens on 127.0.0.1:8443)
./redactor

# Or drop a config.toml in the current directory - auto-detected
./redactor

# Run with explicit config path
./redactor --config /path/to/config.toml

# Run with verbose logging
./redactor --log-level debug
```

### Production
```bash
# Build optimized binary
go build -ldflags="-s -w" -o redactor ./cmd/redactor

# Run with production config (auto-detected from ./config.toml or specified explicitly)
./redactor --config /etc/redactor/config.toml --log-level info

# Run as systemd service (see systemd section below)
sudo systemctl start redactor
```

## Configuration

The proxy looks for `config.toml` in the current directory on startup. If found, it is used automatically. Otherwise, built-in defaults apply (listen on `127.0.0.1:8443`, all detectors enabled except `person_name`).

You can override this with the `--config` flag to specify any path.

Create `config.toml`:
```toml
[listen]
address = "127.0.0.1:8443"

[pii]
enable_email = true
enable_phone = true
enable_creditcard = true
enable_ssn = true
enable_ip = true
enable_path = true
enable_apikey = true
enable_domain = true
enable_person_name = false  # High false-positive rate, opt-in only
enable_client_name = true
log_redacted = false  # Log each redacted item (placeholder, category, original value) at info level
log_bodies = false  # Log full request/response bodies at info level when enabled (verbose)

[pii.client_names]
names = ["Acme Corp", "Project Phoenix", "Internal API"]

[upstream]
# Override default upstream URLs if needed
[upstream.openai]
host_match = "api.openai.com"
base_url = "https://api.openai.com"

[ui]
enable = true  # Serve the real-time log viewer at /logs (default: true)
```

## Usage with AI CLIs

### Claude Code
```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:8443
claude
```

### OpenAI CLI
```bash
export OPENAI_BASE_URL=http://127.0.0.1:8443
openai-cli
```

### Gemini CLI
```bash
# Set base URL to proxy
gemini-cli --base-url=http://127.0.0.1:8443
```

## Health Check
```bash
curl http://127.0.0.1:8443/healthz
```

## Live Log Viewer

The proxy serves a real-time log viewer on the same port as the proxy itself.
Open `http://127.0.0.1:8443/logs` in a browser to watch the proxy's structured
JSON logs as they are emitted.

- Log records are streamed over Server-Sent Events from `/logs/events`.
- The viewer filters entries client-side by level (debug, info, warn, error) and
  by free-text search.
- Assets are embedded in the binary, so the UI adds no runtime files.
- The stream is read-only and shows the same records the proxy writes to stderr.

The viewer is enabled by default. To turn it off, add the following to your
config file:

```toml
[ui]
enable = false
```

When disabled, the `/logs` paths are not registered at all, so requests to them
are handled by the proxy's normal routing exactly like any other path. The
proxy's `--log-level` flag still controls which records are emitted, so use
`--log-level debug` for the most detail.

## Systemd Service (Linux)

Create `/etc/systemd/system/redactor.service`:
```ini
[Unit]
Description=PII Redacting Proxy for AI CLIs
After=network.target

[Service]
Type=simple
User=redactor
WorkingDirectory=/etc/redactor
ExecStart=/usr/local/bin/redactor
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable redactor
sudo systemctl start redactor
```

## How It Works

1. **Request interception**: The proxy receives requests from AI CLIs
2. **PII detection**: Scans JSON bodies for emails, phone numbers, credit cards, SSNs, IPs, file paths, API keys, domains, and custom client names
3. **Redaction**: Replaces detected PII with placeholders (e.g., `<EMAIL_1>`, `<PHONE_2>`)
4. **Forwarding**: Sends sanitized request to the real upstream API
5. **Unredaction**: Restores original PII in the response before returning to the client

All PII mappings are per-request and never logged or persisted.

## Performance

- **SSE parsing**: ~468 MB/s throughput
- **Proxy forwarding**: 27 allocs/op for 1KB request
- **10KB PII redaction**: ~4.5ms (80+ PII instances)
- **Zero PII persistence**: All mappings are in-memory and per-request

## License

MIT
