// Live log stream client for the Redactor UI.
//
// The proxy broadcasts one JSON log record per Server-Sent Event on
// /logs/events. This module owns the EventSource lifecycle, normalizes each
// record into a renderable entry, and keeps the in-browser buffer bounded.
// Components subscribe to the exported stores; the logs route starts the
// connection when it mounts and stops it when it unmounts.
//
// The stream URL is an absolute path rather than a document-relative one: the
// same bundle is served from both /logs/ and /config/, and only /logs/events
// exists on the Go mux, so resolving against the document base would produce
// /config/logs/events when the app was opened at the config mount.
import { writable } from "svelte/store";

// EVENTS_PATH is the SSE endpoint mounted by the Go server.
export const EVENTS_PATH = "/logs/events";

// DefaultMaxEntries bounds how many records the viewer keeps in memory.
export const DefaultMaxEntries = 1000;
const MinMaxEntries = 100;
const MaxMaxEntries = 10000;

const BaseReconnectDelayMs = 1000;
const MaxReconnectDelayMs = 30000;

// List-view preview limits. Anything longer is cut and can be inspected in the
// detail modal, which always renders the full record.
const PreviewMessageLines = 4;
const PreviewFieldLength = 120;

const ReservedKeys = new Set(["time", "level", "msg"]);

const Levels: readonly LogLevel[] = ["debug", "info", "warn", "error"];

export type LogLevel = "debug" | "info" | "warn" | "error";

/** Level filter accepted by the viewer; "all" disables level filtering. */
export type LevelFilter = LogLevel | "all";

/** Connection state surfaced by the header status pill. */
export type ConnectionState =
  | "connecting"
  | "connected"
  | "reconnecting"
  | "disconnected"
  | "unsupported";

/** One JSON log record as emitted by slog's JSON handler. */
export interface LogRecord {
  time?: unknown;
  level?: unknown;
  msg?: unknown;
  [key: string]: unknown;
}

/** A flattened slog attribute (nested groups become dotted keys). */
export interface LogField {
  key: string;
  value: string;
}

/** Timestamp rendered three ways: compact, machine-readable and original. */
export interface TimeInfo {
  display: string;
  iso: string;
  title: string;
}

/** One normalized log entry ready for rendering. */
export interface LogEntry {
  id: number;
  record: LogRecord;
  level: LogLevel;
  time: TimeInfo;
  message: string;
  messagePreview: string;
  messageTruncated: boolean;
  fields: LogField[];
  /** True when the row hides content that the modal reveals. */
  truncated: boolean;
  /** Lowercased text used by the search filter. */
  haystack: string;
}

// ---------------------------------------------------------------- formatting

function normalizeLevel(raw: unknown): LogLevel {
  if (typeof raw === "number" && Number.isFinite(raw)) {
    // slog's standard levels and common custom offsets.
    if (raw <= -4) return "debug";
    if (raw < 4) return "info";
    if (raw < 8) return "warn";
    return "error";
  }
  const value = String(raw ?? "").toLowerCase();
  if ((Levels as readonly string[]).includes(value)) return value as LogLevel;
  if (value === "warning") return "warn";
  if (value === "err" || value === "fatal" || value === "panic" || value === "critical") {
    return "error";
  }
  if (value === "trace" || value === "verbose") return "debug";
  return "info";
}

function pad(value: number, width = 2): string {
  return String(value).padStart(width, "0");
}

// formatTime returns a compact local wall-clock time for the row, the
// machine-readable timestamp, and the original value for a tooltip.
// Unparseable timestamps are shown verbatim.
function formatTime(raw: unknown): TimeInfo {
  if (raw === null || raw === undefined || raw === "") {
    return { display: "", iso: "", title: "" };
  }
  const original = String(raw);
  const date = new Date(original);
  if (Number.isNaN(date.getTime())) {
    return { display: original, iso: "", title: original };
  }
  const display =
    pad(date.getHours()) +
    ":" +
    pad(date.getMinutes()) +
    ":" +
    pad(date.getSeconds()) +
    "." +
    pad(date.getMilliseconds(), 3);
  return { display, iso: date.toISOString(), title: original };
}

function formatValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (value === null) return "null";
  if (typeof value === "object") {
    try {
      return JSON.stringify(value);
    } catch {
      return String(value);
    }
  }
  return String(value);
}

// collectFields flattens nested slog groups into dotted key/value pairs.
function collectFields(
  value: Record<string, unknown>,
  prefix: string,
  out: LogField[],
): LogField[] {
  for (const key of Object.keys(value)) {
    const child = value[key];
    const name = prefix ? `${prefix}.${key}` : key;
    if (child !== null && typeof child === "object" && !Array.isArray(child)) {
      collectFields(child as Record<string, unknown>, name, out);
    } else {
      out.push({ key: name, value: formatValue(child) });
    }
  }
  return out;
}

// recordFields returns the non-reserved fields of a record as flattened
// dotted key/value pairs, ready for both the row preview and the modal.
function recordFields(record: LogRecord): LogField[] {
  const attrs: Record<string, unknown> = {};
  for (const key of Object.keys(record)) {
    if (!ReservedKeys.has(key)) attrs[key] = record[key];
  }
  return collectFields(attrs, "", []);
}

function buildEntry(record: LogRecord, id: number): LogEntry {
  const level = normalizeLevel(record.level);
  const time = formatTime(record.time);
  const message = record.msg === null || record.msg === undefined ? "" : formatValue(record.msg);

  const lines = message.split("\n");
  const messageTruncated = lines.length > PreviewMessageLines;
  const messagePreview = messageTruncated
    ? lines.slice(0, PreviewMessageLines).join("\n")
    : message;

  const fields = recordFields(record);
  const fieldsTruncated = fields.some((field) => field.value.length > PreviewFieldLength);

  const haystackParts: string[] = [time.display, level, message];
  for (const field of fields) haystackParts.push(field.key, field.value);

  return {
    id,
    record,
    level,
    time,
    message,
    messagePreview,
    messageTruncated,
    fields,
    truncated: messageTruncated || fieldsTruncated,
    haystack: haystackParts.join(" ").toLowerCase(),
  };
}

// resolveEntryLimit reads the optional ?max= query parameter. The hash router
// keeps application query strings in the fragment, so both forms are checked.
function resolveEntryLimit(): number {
  if (typeof window === "undefined") return DefaultMaxEntries;

  let raw = new URLSearchParams(window.location.search).get("max");
  if (raw === null) {
    const hash = window.location.hash;
    const queryStart = hash.indexOf("?");
    if (queryStart !== -1) {
      raw = new URLSearchParams(hash.slice(queryStart + 1)).get("max");
    }
  }

  const parsed = raw === null ? Number.NaN : Number.parseInt(raw, 10);
  if (!Number.isFinite(parsed)) return DefaultMaxEntries;
  return Math.min(MaxMaxEntries, Math.max(MinMaxEntries, parsed));
}

/** Maximum number of entries retained by the viewer. */
export const entryLimit = resolveEntryLimit();

// -------------------------------------------------------------------- stores

/** Log entries in arrival order, most recent last. */
export const entries = writable<LogEntry[]>([]);
/** Number of entries lost to the server queue or the local entry cap. */
export const dropped = writable(0);
/** Current SSE connection state. */
export const connection = writable<ConnectionState>("connecting");
/** Seconds remaining before the next reconnect attempt, when counting down. */
export const reconnectIn = writable(0);

// ---------------------------------------------------------------- controller

// LogStream owns the EventSource for the logs route. A singleton is enough:
// the SPA shows at most one log viewer at a time.
class LogStream {
  #source: EventSource | null = null;
  #reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  #attempts = 0;
  #nextId = 1;
  #active = false;

  /** start opens the stream, or does nothing if it is already running. */
  start(): void {
    if (this.#active) return;
    this.#active = true;
    this.connect();
  }

  /** stop closes the stream and cancels any pending reconnect. */
  stop(): void {
    this.#active = false;
    this.clearReconnectTimer();
    if (this.#source) {
      this.#source.close();
      this.#source = null;
    }
    reconnectIn.set(0);
    connection.set("disconnected");
  }

  /** clear empties the entry buffer without touching the connection. */
  clear(): void {
    entries.set([]);
  }

  private connect(): void {
    this.clearReconnectTimer();
    if (this.#source) {
      this.#source.close();
      this.#source = null;
    }

    if (typeof EventSource === "undefined") {
      connection.set("unsupported");
      return;
    }

    connection.set("connecting");
    reconnectIn.set(0);

    let source: EventSource;
    try {
      source = new EventSource(EVENTS_PATH);
    } catch {
      connection.set("disconnected");
      this.scheduleReconnect();
      return;
    }
    this.#source = source;

    source.onopen = () => {
      if (this.#source !== source) return;
      this.#attempts = 0;
      reconnectIn.set(0);
      connection.set("connected");
    };

    source.onerror = () => {
      if (this.#source !== source) return;
      // While the browser is still CONNECTING it retries on its own; opening a
      // second connection here would duplicate every event. Only take over
      // once the source has permanently closed.
      if (source.readyState === EventSource.CLOSED) {
        connection.set("disconnected");
        this.scheduleReconnect();
      } else {
        connection.set("reconnecting");
      }
    };

    source.onmessage = (event) => this.handleLine(event.data);
    source.addEventListener("dropped", (event) =>
      this.handleDropped((event as MessageEvent<string>).data),
    );
  }

  // scheduleReconnect retries only after the browser has permanently closed
  // the EventSource, backing off exponentially up to the maximum delay.
  private scheduleReconnect(): void {
    if (!this.#active || this.#reconnectTimer) return;

    if (this.#source) {
      this.#source.close();
      this.#source = null;
    }

    this.#attempts += 1;
    const delay = Math.min(
      MaxReconnectDelayMs,
      BaseReconnectDelayMs * 2 ** Math.min(this.#attempts - 1, 5),
    );

    connection.set("reconnecting");
    reconnectIn.set(Math.ceil(delay / 1000));
    this.#reconnectTimer = setTimeout(() => {
      this.#reconnectTimer = null;
      reconnectIn.set(0);
      this.connect();
    }, delay);
  }

  private clearReconnectTimer(): void {
    if (this.#reconnectTimer === null) return;
    clearTimeout(this.#reconnectTimer);
    this.#reconnectTimer = null;
  }

  private handleLine(raw: string): void {
    let parsed: unknown;
    try {
      parsed = JSON.parse(raw);
    } catch {
      this.addEntry({
        time: new Date().toISOString(),
        level: "warn",
        msg: "Unparsed log line",
        raw,
      });
      return;
    }

    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
      this.addEntry({
        time: new Date().toISOString(),
        level: "warn",
        msg: "Unexpected log payload",
        raw,
      });
      return;
    }

    this.addEntry(parsed as LogRecord);
  }

  private addEntry(record: LogRecord): void {
    const entry = buildEntry(record, this.#nextId++);

    let evicted = 0;
    entries.update((list) => {
      const next = [...list, entry];
      if (next.length > entryLimit) {
        evicted = next.length - entryLimit;
        return next.slice(evicted);
      }
      return next;
    });

    if (evicted > 0) {
      dropped.update((count) => count + evicted);
    }
  }

  private handleDropped(raw: string): void {
    const amount = parseDropped(raw);
    if (amount > 0) {
      dropped.update((count) => count + amount);
    }
  }
}

function parseDropped(raw: string): number {
  const text = String(raw ?? "").trim();
  if (text === "") return 1;
  try {
    const parsed: unknown = JSON.parse(text);
    if (typeof parsed === "number" && Number.isFinite(parsed)) {
      return Math.max(0, Math.trunc(parsed));
    }
    if (parsed !== null && typeof parsed === "object") {
      const candidate = parsed as { dropped?: unknown; count?: unknown };
      const value = candidate.dropped ?? candidate.count;
      if (typeof value === "number" && Number.isFinite(value)) {
        return Math.max(0, Math.trunc(value));
      }
    }
  } catch {
    // Fall through to the plain-number form.
  }
  const numeric = Number(text);
  if (Number.isFinite(numeric) && numeric > 0) return Math.trunc(numeric);
  return 1;
}

/** Process-wide log stream controller used by the logs route. */
export const logStream = new LogStream();
