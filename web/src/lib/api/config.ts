// HTTP client for the proxy's configuration API.
//
// The endpoints are mounted at the origin root rather than beneath the SPA
// mount, so every path here is absolute. Successful responses are JSON config
// documents; failures use {"error": "...", "field": "..."} where field names
// the offending config key.
//
// The JSON field names mirror the Go struct tags in internal/config, so the
// document can be sent back verbatim after editing. Only the per-category
// replacement fields and the two logging flags are exposed by the editor; the
// rest of the document round-trips untouched.

/** Replacement strategy used for a redaction category. */
export type ReplacementMode = "placeholder" | "custom";

/** Category blocks stored under `[pii.<category>]`. */
export interface CategoryConfig {
  /** Overrides the flat enable flag when present. */
  enabled?: boolean | null;
  names?: string[] | null;
  patterns?: string[] | null;
  denylist?: string[] | null;
  /** Defaults to "placeholder" when omitted. */
  mode?: ReplacementMode | null;
  /** Required when mode is "custom"; ignored otherwise. */
  text?: string | null;
}

/** Legacy `[pii.client_names]` block, merged into `pii.client.names` on load. */
export interface ClientNamesConfig {
  names?: string[] | null;
}

/** All redaction categories, in the order the editor presents them. */
export const PII_CATEGORY_KEYS = [
  "email",
  "phone",
  "credit_card",
  "ssn",
  "ip",
  "path",
  "api_key",
  "credential",
  "domain",
  "client",
  "person_name",
] as const;

/** A redaction category key, matching its `[pii.<key>]` block. */
export type PIICategoryKey = (typeof PII_CATEGORY_KEYS)[number];

/** `[pii]` section of the configuration document. */
export interface PIIConfig {
  enable_email: boolean;
  enable_phone: boolean;
  enable_creditcard: boolean;
  enable_ssn: boolean;
  enable_ip: boolean;
  enable_path: boolean;
  enable_apikey: boolean;
  enable_credential: boolean;
  enable_domain: boolean;
  enable_person_name: boolean;
  enable_client_name: boolean;

  email: CategoryConfig;
  phone: CategoryConfig;
  credit_card: CategoryConfig;
  ssn: CategoryConfig;
  ip: CategoryConfig;
  path: CategoryConfig;
  api_key: CategoryConfig;
  credential: CategoryConfig;
  domain: CategoryConfig;
  client: CategoryConfig;
  person_name: CategoryConfig;

  client_names: ClientNamesConfig;
  log_redacted: boolean;
  log_bodies: boolean;
}

export interface ListenConfig {
  address: string;
}

export interface UpstreamConfig {
  host_match: string;
  base_url: string;
}

export interface UIConfig {
  enable: boolean;
}

/** Full configuration document returned by GET /api/config. */
export interface AppConfig {
  listen: ListenConfig;
  pii: PIIConfig;
  upstream: Record<string, UpstreamConfig>;
  ui: UIConfig;
}

/** One redaction reported by the preview endpoint. */
export interface PreviewMatch {
  category: string;
  start: number;
  end: number;
  replacement: string;
}

/** Response body of POST /api/config/preview. */
export interface PreviewResponse {
  redacted: string;
  matches: PreviewMatch[];
}

/** Error response shape used by every config endpoint. */
interface ApiErrorBody {
  error?: string;
  field?: string;
}

/** Error carrying the HTTP status and optional offending config field. */
export class ConfigApiError extends Error {
  readonly status: number;
  readonly field: string;

  constructor(message: string, status = 0, field = "") {
    super(message);
    this.name = "ConfigApiError";
    this.status = status;
    this.field = field;
  }
}

/** Best-effort human-readable message for an unknown thrown value. */
export function errorMessage(error: unknown): string {
  if (error instanceof Error && error.message.trim() !== "") return error.message;
  return "Unexpected error.";
}

/** True when a fetch was aborted because a newer request superseded it. */
export function isAbortError(error: unknown): boolean {
  return (
    typeof error === "object" &&
    error !== null &&
    (error as { name?: unknown }).name === "AbortError"
  );
}

// compactList drops empty entries but preserves the exact value of the rest.
// The editor's add control trims its own input; an entry cleared in place
// should not be sent as an empty name or expression.
function compactList(items: string[] | null | undefined): string[] {
  return (items ?? []).filter((item) => item.trim() !== "");
}

// toWireConfig prepares the editor state for PUT and preview requests.
//
// 1. The legacy [pii.client_names] block is emptied: GET returns it alongside
//    the already-merged [pii.client].names, and the loader merges it again on
//    decode, so sending it back would resurrect names removed in the editor.
// 2. Blank list entries are dropped.
//
// Everything else is forwarded as-is so parts of the document the editor does
// not expose (listen, upstream, ui) survive a save.
export function toWireConfig(config: AppConfig): AppConfig {
  const pii: PIIConfig = { ...config.pii, client_names: { names: [] } };
  for (const key of PII_CATEGORY_KEYS) {
    const category = pii[key];
    pii[key] = {
      ...category,
      names: compactList(category.names),
      patterns: compactList(category.patterns),
      denylist: compactList(category.denylist),
    };
  }
  return { ...config, pii };
}

interface RequestInitLite {
  method?: string;
  body?: string;
  signal?: AbortSignal;
}

async function request<T>(path: string, init: RequestInitLite = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (init.body !== undefined) headers["Content-Type"] = "application/json";

  let response: Response;
  try {
    response = await fetch(path, {
      method: init.method ?? "GET",
      headers,
      body: init.body,
      signal: init.signal,
      cache: "no-store",
    });
  } catch (error) {
    if (isAbortError(error)) throw error;
    throw new ConfigApiError(
      "Could not reach the configuration API. Is the proxy running?",
      0,
    );
  }

  const text = await response.text();
  let body: unknown = null;
  if (text !== "") {
    try {
      body = JSON.parse(text);
    } catch {
      // Leave body null so the status-based message is used. A 404 served by
      // the proxy catch-all is HTML, for example.
    }
  }

  if (!response.ok) {
    const failure = body as ApiErrorBody | null;
    const message =
      typeof failure?.error === "string" && failure.error !== ""
        ? failure.error
        : `The configuration API returned HTTP ${response.status}.`;
    const field = typeof failure?.field === "string" ? failure.field : "";
    throw new ConfigApiError(message, response.status, field);
  }

  if (body === null) {
    throw new ConfigApiError(
      "The configuration API returned an empty response.",
      response.status,
    );
  }

  return body as T;
}

/** GET /api/config */
export function getConfig(signal?: AbortSignal): Promise<AppConfig> {
  return request<AppConfig>("/api/config", { signal });
}

/** PUT /api/config */
export function putConfig(config: AppConfig): Promise<AppConfig> {
  return request<AppConfig>("/api/config", {
    method: "PUT",
    body: JSON.stringify(toWireConfig(config)),
  });
}

/** POST /api/config/reload */
export function reloadConfig(): Promise<AppConfig> {
  return request<AppConfig>("/api/config/reload", { method: "POST" });
}

/** POST /api/config/preview with the current editor state. */
export function fetchPreview(
  text: string,
  config: AppConfig,
  signal?: AbortSignal,
): Promise<PreviewResponse> {
  return request<PreviewResponse>("/api/config/preview", {
    method: "POST",
    body: JSON.stringify({ text, config: toWireConfig(config) }),
    signal,
  });
}
