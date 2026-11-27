// Configuration editor state.
//
// The store owns the full document returned by GET /api/config together with
// the baseline it was loaded from. Keeping the whole document (rather than just
// the edited fields) means a PUT preserves sections the editor does not expose,
// such as listen, upstream, and the UI toggle.
//
// Components mutate the form through the helpers below; saving validates the
// form, PUTs the document, and then POSTs /api/config/reload so the running
// proxy re-reads what was just written to disk.

import { derived, get, writable } from "svelte/store";
import {
  ConfigApiError,
  PII_CATEGORY_KEYS,
  errorMessage,
  getConfig,
  putConfig,
  reloadConfig,
  toWireConfig,
  type AppConfig,
  type CategoryConfig,
  type PIICategoryKey,
  type PIIConfig,
  type ReplacementMode,
} from "$lib/api/config";

/** Flat enable flag paired with each per-category block. */
export type EnableFlagKey =
  | "enable_email"
  | "enable_phone"
  | "enable_creditcard"
  | "enable_ssn"
  | "enable_ip"
  | "enable_path"
  | "enable_apikey"
  | "enable_credential"
  | "enable_domain"
  | "enable_person_name"
  | "enable_client_name";

/** Presentation metadata for one redaction category. */
export interface CategoryMeta {
  key: PIICategoryKey;
  label: string;
  /** Flat [pii] flag the category block overrides. */
  toggle: EnableFlagKey;
  /** Default placeholder prefix shown in help text. */
  placeholder: string;
  summary: string;
}

/** Category metadata in the order the editor lists categories. */
export const CATEGORY_META: readonly CategoryMeta[] = [
  {
    key: "email",
    label: "Email",
    toggle: "enable_email",
    placeholder: "<EMAIL_n>",
    summary: "Email addresses",
  },
  {
    key: "phone",
    label: "Phone",
    toggle: "enable_phone",
    placeholder: "<PHONE_n>",
    summary: "Phone numbers in international and local formats",
  },
  {
    key: "credit_card",
    label: "Credit card",
    toggle: "enable_creditcard",
    placeholder: "<CC_n>",
    summary: "Card numbers that pass the Luhn check",
  },
  {
    key: "ssn",
    label: "SSN",
    toggle: "enable_ssn",
    placeholder: "<SSN_n>",
    summary: "US social security numbers",
  },
  {
    key: "ip",
    label: "IP address",
    toggle: "enable_ip",
    placeholder: "<IP_n>",
    summary: "IPv4 and IPv6 addresses",
  },
  {
    key: "path",
    label: "File path",
    toggle: "enable_path",
    placeholder: "<PATH_n>",
    summary: "Filesystem paths",
  },
  {
    key: "api_key",
    label: "API key",
    toggle: "enable_apikey",
    placeholder: "<APIKEY_n>",
    summary: "Provider-specific and generic API keys",
  },
  {
    key: "credential",
    label: "Credential",
    toggle: "enable_credential",
    placeholder: "<CRED_n>",
    summary: "Passwords and secrets in key=value pairs and URLs",
  },
  {
    key: "domain",
    label: "Domain",
    toggle: "enable_domain",
    placeholder: "<DOMAIN_n>",
    summary: "Registrable domains; subdomains keep their leading label",
  },
  {
    key: "client",
    label: "Client name",
    toggle: "enable_client_name",
    placeholder: "<CLIENT_n>",
    summary: "Literal client and project names",
  },
  {
    key: "person_name",
    label: "Person name",
    toggle: "enable_person_name",
    placeholder: "<NAME_n>",
    summary: "Opt-in person-name heuristic; noisy on prose",
  },
];

/** Metadata lookup keyed by category. */
export const CATEGORY_META_BY_KEY = Object.fromEntries(
  CATEGORY_META.map((meta) => [meta.key, meta]),
) as Record<PIICategoryKey, CategoryMeta>;

/** Editable list inside a category block. */
export type CategoryListField = "names" | "patterns" | "denylist";

/** Logging flags exposed in the editor. */
export type LogFlagKey = "log_redacted" | "log_bodies";

export type LoadStatus = "loading" | "ready" | "error";

/** Everything the editor views need to render. */
export interface ConfigState {
  status: LoadStatus;
  /** Editable form state; null until the first load succeeds. */
  config: AppConfig | null;
  /** Last loaded or saved document, used to detect unsaved changes. */
  baseline: AppConfig | null;
  /** Last API error, shown as a banner. */
  error: string | null;
  /** Config field named by the last API error, when known. */
  field: string;
  /** True while a save or reload request is in flight. */
  busy: boolean;
  /** Success message for the most recent save or reload. */
  notice: string | null;
}

const initialState: ConfigState = {
  status: "loading",
  config: null,
  baseline: null,
  error: null,
  field: "",
  busy: false,
  notice: null,
};

/** Single config document shared by the editor view and its children. */
export const configStore = writable<ConfigState>(initialState);

// ------------------------------------------------------------------ helpers

/** Effective enabled state for a category: block override, else flat flag. */
export function categoryEnabled(pii: PIIConfig, meta: CategoryMeta): boolean {
  const category = pii[meta.key];
  if (category.enabled !== null && category.enabled !== undefined) {
    return category.enabled;
  }
  return pii[meta.toggle];
}

/** Effective replacement mode; an omitted mode means placeholder. */
export function categoryMode(category: CategoryConfig): ReplacementMode {
  return category.mode === "custom" ? "custom" : "placeholder";
}

/** Replacement text with null normalized to the empty string. */
export function categoryText(category: CategoryConfig): string {
  return category.text ?? "";
}

/** One editable list of a category, normalized to an array. */
export function categoryList(
  category: CategoryConfig,
  field: CategoryListField,
): string[] {
  return category[field] ?? [];
}

/** Category named by an API validation field such as "pii.email.patterns[0]". */
export function categoryFromField(field: string): PIICategoryKey | null {
  const match = /^pii\.([a-z_]+)(?:\.|\[|$)/.exec(field);
  if (match === null) return null;
  const key = match[1] as PIICategoryKey;
  return PII_CATEGORY_KEYS.includes(key) ? key : null;
}

function cloneConfig(config: AppConfig): AppConfig {
  return JSON.parse(JSON.stringify(config)) as AppConfig;
}

function signature(config: AppConfig): string {
  return JSON.stringify(toWireConfig(config));
}

function apiField(error: unknown): string {
  return error instanceof ConfigApiError ? error.field : "";
}

function updateConfig(mutate: (config: AppConfig) => AppConfig): void {
  configStore.update((state) => {
    if (state.config === null) return state;
    return {
      ...state,
      config: mutate(state.config),
      // Any edit invalidates the last success notice.
      notice: null,
    };
  });
}

function updateCategory(
  key: PIICategoryKey,
  patch: Partial<CategoryConfig>,
): void {
  updateConfig((config) => ({
    ...config,
    pii: {
      ...config.pii,
      [key]: { ...config.pii[key], ...patch },
    } as PIIConfig,
  }));
}

// -------------------------------------------------------------------- actions

/** Load the current configuration from the proxy. */
export async function loadConfig(): Promise<void> {
  configStore.update((state) => ({
    ...state,
    status: state.config === null ? "loading" : state.status,
    error: null,
    field: "",
  }));

  try {
    const config = await getConfig();
    configStore.set({
      status: "ready",
      config,
      baseline: cloneConfig(config),
      error: null,
      field: "",
      busy: false,
      notice: null,
    });
  } catch (error) {
    configStore.update((state) => ({
      ...state,
      status: state.config === null ? "error" : state.status,
      error: errorMessage(error),
      field: apiField(error),
    }));
  }
}

/** Enable or disable one category, keeping the flat toggle in sync. */
export function setCategoryEnabled(
  key: PIICategoryKey,
  enabled: boolean,
): void {
  const meta = CATEGORY_META_BY_KEY[key];
  updateConfig((config) => {
    const pii = {
      ...config.pii,
      [meta.toggle]: enabled,
      [key]: { ...config.pii[key], enabled },
    } as PIIConfig;
    return { ...config, pii };
  });
}

/** Change a category's replacement mode. */
export function setCategoryMode(
  key: PIICategoryKey,
  mode: ReplacementMode,
): void {
  updateCategory(key, { mode });
}

/** Change a category's custom replacement text. */
export function setCategoryText(key: PIICategoryKey, text: string): void {
  updateCategory(key, { text });
}

/** Replace one editable list of a category. */
export function setCategoryList(
  key: PIICategoryKey,
  field: CategoryListField,
  items: string[],
): void {
  updateCategory(key, { [field]: items });
}

/** Toggle one of the logging flags. */
export function setLogFlag(flag: LogFlagKey, value: boolean): void {
  updateConfig((config) => ({
    ...config,
    pii: { ...config.pii, [flag]: value },
  }));
}

/** Clear the current error banner. */
export function dismissError(): void {
  configStore.update((state) => ({ ...state, error: null, field: "" }));
}

/** Clear the current success notice. */
export function dismissNotice(): void {
  configStore.update((state) => ({ ...state, notice: null }));
}

// ----------------------------------------------------------------- validation

export interface ConfigValidation {
  ok: boolean;
  /** Human-readable issues, in category order. */
  messages: string[];
  /** Per-category message for inline display. */
  categories: Partial<Record<PIICategoryKey, string>>;
  /** API-style field of the first issue, used to highlight it on save. */
  firstField: string;
}

/** Validate the parts of the form the editor can make invalid. */
export function validateConfig(config: AppConfig): ConfigValidation {
  const categories: Partial<Record<PIICategoryKey, string>> = {};
  const messages: string[] = [];
  let firstField = "";

  for (const meta of CATEGORY_META) {
    const category = config.pii[meta.key];
    const mode = category.mode ?? "";

    if (mode !== "" && mode !== "placeholder" && mode !== "custom") {
      const message = `Unknown replacement mode "${String(mode)}".`;
      categories[meta.key] = message;
      messages.push(`${meta.label}: ${message}`);
      if (firstField === "") firstField = `pii.${meta.key}.mode`;
      continue;
    }

    if (mode === "custom" && (category.text ?? "").trim() === "") {
      const message = "Custom replacement text is required.";
      categories[meta.key] = message;
      messages.push(`${meta.label}: ${message}`);
      if (firstField === "") firstField = `pii.${meta.key}.text`;
    }
  }

  return {
    ok: messages.length === 0,
    messages,
    categories,
    firstField,
  };
}

/** True when the form differs from the last loaded or saved document. */
export const dirty = derived(configStore, (state) => {
  if (state.config === null || state.baseline === null) return false;
  return signature(state.config) !== signature(state.baseline);
});

/** True when a logging flag differs from the baseline. */
export const logSettingsChanged = derived(configStore, (state) => {
  if (state.config === null || state.baseline === null) return false;
  return (
    state.config.pii.log_redacted !== state.baseline.pii.log_redacted ||
    state.config.pii.log_bodies !== state.baseline.pii.log_bodies
  );
});

/** Client-side validation of the current form. */
export const validation = derived(configStore, (state) =>
  state.config === null
    ? { ok: true, messages: [], categories: {}, firstField: "" }
    : validateConfig(state.config),
);

// --------------------------------------------------------------------- saving

/**
 * Save the form, then reload the proxy from disk.
 *
 * Resolves true when the document was written, including the case where the
 * subsequent reload failed (the PUT itself applies the config, so the edit did
 * take effect; the failure is surfaced as an error banner).
 */
export async function saveConfig(): Promise<boolean> {
  const state = get(configStore);
  if (state.config === null) return false;

  const check = validateConfig(state.config);
  if (!check.ok) {
    configStore.update((current) => ({
      ...current,
      error: check.messages[0] ?? "Fix the highlighted fields before saving.",
      field: check.firstField,
    }));
    return false;
  }

  configStore.update((current) => ({
    ...current,
    busy: true,
    error: null,
    field: "",
    notice: null,
  }));

  let saved: AppConfig;
  try {
    saved = await putConfig(state.config);
  } catch (error) {
    configStore.update((current) => ({
      ...current,
      busy: false,
      error: errorMessage(error),
      field: apiField(error),
    }));
    return false;
  }

  let applied = saved;
  let reloadError = "";
  try {
    applied = await reloadConfig();
  } catch (error) {
    reloadError = errorMessage(error);
  }

  configStore.set({
    status: "ready",
    config: cloneConfig(applied),
    baseline: cloneConfig(applied),
    error:
      reloadError === ""
        ? null
        : `Saved, but reloading from disk failed: ${reloadError}`,
    field: "",
    busy: false,
    notice: reloadError === "" ? "Configuration saved and reloaded." : null,
  });
  return true;
}

/** Re-read config.toml from disk, discarding any unsaved form state. */
export async function reloadFromDisk(): Promise<boolean> {
  configStore.update((state) => ({
    ...state,
    busy: true,
    error: null,
    field: "",
    notice: null,
  }));

  try {
    const config = await reloadConfig();
    configStore.set({
      status: "ready",
      config: cloneConfig(config),
      baseline: cloneConfig(config),
      error: null,
      field: "",
      busy: false,
      notice: "Configuration reloaded from disk.",
    });
    return true;
  } catch (error) {
    configStore.update((state) => ({
      ...state,
      busy: false,
      error: errorMessage(error),
      field: apiField(error),
    }));
    return false;
  }
}
