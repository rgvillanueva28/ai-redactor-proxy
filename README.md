# PII Redacting Proxy for AI CLIs

A high-performance HTTP proxy that sanitizes PII from AI CLI requests before forwarding to upstream providers (OpenAI, Anthropic, Gemini). The proxy redacts sensitive data using placeholder tags, forwards the sanitized request, then unredacts the response before returning to the client.

## Quick Start

### Build
```bash
go build -o redactor ./cmd/redactor
```

### Development
```bash
# Run with defaults (listens on 127.0.0.1:28443)
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

The proxy looks for `config.toml` in the current directory on startup. If found, it is used automatically. Otherwise, built-in defaults apply (listen on `127.0.0.1:28443`, all detectors enabled except `person_name`).

You can override this with the `--config` flag to specify any path.

Create `config.toml`:
```toml
[listen]
address = "127.0.0.1:28443"

[pii]
enable_email = true
enable_phone = true
enable_creditcard = true
enable_ssn = true
enable_ip = true
enable_path = true
enable_apikey = true
enable_credential = true
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

### PII categories

Every detector has a flat on/off toggle and its own placeholder prefix. All are
enabled by default except `person_name`, whose heuristic is noisy enough to be
opt-in.

| Category    | Toggle               | Placeholder  | Detects |
|-------------|----------------------|--------------|---------|
| Email       | `enable_email`       | `<EMAIL_n>`  | Email addresses |
| Phone       | `enable_phone`       | `<PHONE_n>`  | Phone numbers in common international and local formats |
| Credit card | `enable_creditcard`  | `<CC_n>`     | Card numbers that pass the Luhn check |
| SSN         | `enable_ssn`         | `<SSN_n>`    | US social security numbers |
| IP          | `enable_ip`          | `<IP_n>`     | IPv4 and IPv6 addresses |
| Path        | `enable_path`        | `<PATH_n>`   | Filesystem paths |
| API key     | `enable_apikey`      | `<APIKEY_n>` | Provider-specific and generic API keys (for example `sk-proj-...`) |
| Credential  | `enable_credential`  | `<CRED_n>`   | Passwords and secrets in `key=value` and query strings, JSON fields, YAML/prose (`password: hunter2`), URL userinfo (`https://user:pass@host`) and bare `user:password` literals |
| Domain      | `enable_domain`      | `<DOMAIN_n>` | Registrable domains; `api.acme.com` becomes `api.<DOMAIN_1>` |
| Client name | `enable_client_name` | `<CLIENT_n>` | Literal names from `[pii.client].names` |
| Person name | `enable_person_name` | `<NAME_n>`   | Person-name heuristic (opt-in) |

The credential detector reports only the secret, not the surrounding label, so
`password=hunter2` becomes `password=<CRED_1>` and the text stays readable. It
does not report a bare `user:password` pair when the right-hand side is all
digits (more likely a `host:port`) or when the left-hand side is a known
credential label such as `password`.

### Per-category customization

Every category also accepts an optional `[pii.<category>]` block that can
override the flat toggle and extend or restrict what the detector reports:

```toml
[pii.email]
enabled = true                        # overrides enable_email
names = ["noreply@company.com"]       # extra literal values
patterns = ["\\bINTERNAL-\\d{4}\\b"]  # extra regular expressions
denylist = ["public@example.com"]     # values that must never be redacted
```

- `enabled` overrides the matching flat toggle in both directions, so
  `[pii.email] enabled = false` wins over `enable_email = true`.
- `names` are literal, case-insensitive values stored in an Aho-Corasick
  dictionary, so long lists stay cheap.
- `patterns` are Go regular expressions. An invalid pattern is skipped and the
  rest of the category keeps working.
- `denylist` lists exact values that are never redacted. It applies to built-in
  matches and to `names`/`patterns` hits alike.
- Everything a category reports keeps that category's placeholder prefix, so a
  custom `[pii.phone]` pattern still produces `<PHONE_n>`.

The block name is the lowercase form of the category: `email`, `phone`,
`credit_card`, `ssn`, `ip`, `path`, `api_key`, `credential`, `domain`,
`client`, `person_name`.

Examples:

```toml
# Turn one category off without touching any other toggle.
[pii.ssn]
enabled = false

# Add internal hostnames the publicsuffix-based detector cannot infer, while
# keeping one public domain readable and still redacting the rest.
[pii.domain]
names = ["internal.corp", "build.corp"]
denylist = ["example.com"]

# Recognise an application-specific credential format.
[pii.credential]
patterns = ["\\bAPI-SECRET-[A-Z0-9]+\\b"]

# Add an address plus an internal label that is not address-shaped.
[pii.email]
names = ["noreply@company.com", "Corporate Mailbox"]
```

Backward compatibility: the older `[pii.client_names]` block still works. Its
`names` are merged into `[pii.client].names` at load time, with entries from
`[pii.client]` first and duplicates removed.

TOML note: in basic (double-quoted) strings a backslash must be doubled, hence
`"\\bINTERNAL-\\d{4}\\b"`. Literal (single-quoted) strings keep backslashes
as written: `patterns = ['\bINTERNAL-\d{4}\b']`.

## Usage with AI CLIs

### Claude Code
```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:28443
claude
```

### OpenAI CLI
```bash
export OPENAI_BASE_URL=http://127.0.0.1:28443
openai-cli
```

### Gemini CLI
```bash
# Set base URL to proxy
gemini-cli --base-url=http://127.0.0.1:28443
```

## Health Check
```bash
curl http://127.0.0.1:28443/healthz
```

## Live Log Viewer

The proxy serves a real-time log viewer on the same port as the proxy itself.
Open `http://127.0.0.1:28443/logs` in a browser to watch the proxy's structured
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
2. **PII detection**: Scans JSON bodies for emails, phone numbers, credit cards, SSNs, IPs, file paths, API keys, credentials, domains, and custom client names
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
