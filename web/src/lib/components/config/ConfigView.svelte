<script lang="ts">
  import { onMount } from "svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import type { PIICategoryKey } from "$lib/api/config";
  import {
    CATEGORY_META,
    categoryEnabled,
    categoryFromField,
    configStore,
    dirty,
    dismissError,
    dismissNotice,
    loadConfig,
    logSettingsChanged,
    reloadFromDisk,
    saveConfig,
    setCategoryEnabled,
    setCategoryList,
    setCategoryMode,
    setCategoryText,
    setLogFlag,
    validation,
    type CategoryListField,
    type LogFlagKey,
  } from "$lib/stores/config";
  import CategoryEditor from "./CategoryEditor.svelte";
  import LivePreview from "./LivePreview.svelte";
  import SaveButton from "./SaveButton.svelte";

  const state = $derived($configStore);
  const config = $derived(state.config);
  const check = $derived($validation);
  const isDirty = $derived($dirty);
  const logChanged = $derived($logSettingsChanged);
  const fieldCategory = $derived(categoryFromField(state.field));
  const enabledCount = $derived(
    config === null
      ? 0
      : CATEGORY_META.filter((meta) => categoryEnabled(config.pii, meta)).length,
  );

  onMount(() => {
    if ($configStore.config === null) void loadConfig();
  });

  function logValue(flag: LogFlagKey): boolean {
    return config?.pii[flag] ?? false;
  }

  function handleList(
    key: PIICategoryKey,
    field: CategoryListField,
    items: string[],
  ): void {
    setCategoryList(key, field, items);
  }

  function categoryError(key: PIICategoryKey): string {
    const inline = check.categories[key];
    if (inline !== undefined) return inline;
    if (fieldCategory === key && state.error !== null) return state.error;
    return "";
  }
</script>

<div class="h-full overflow-y-auto">
  <div class="mx-auto w-full max-w-6xl px-4 py-6 sm:px-6 lg:py-8">
    <header
      class="flex flex-col gap-4 border-b border-border/70 pb-5 sm:flex-row sm:items-start sm:justify-between"
    >
      <div class="min-w-0">
        <h1 class="text-xl font-semibold tracking-tight">Configuration</h1>
        <p class="mt-1 max-w-2xl text-sm text-muted-foreground">
          Edit detection categories, replacement styles, and logging. Changes are
          written to
          <code class="rounded bg-muted/60 px-1 py-0.5 font-mono text-xs text-foreground/80">
            config.toml
          </code>
          and applied without restarting the proxy.
        </p>
        <div class="mt-2 flex flex-wrap items-center gap-2 text-[11px]">
          {#if state.busy}
            <span class="inline-flex items-center gap-1.5 text-muted-foreground" role="status">
              <span class="h-2 w-2 animate-pulse rounded-full bg-amber-500" aria-hidden="true"></span>
              Working…
            </span>
          {:else if isDirty}
            <span
              class="rounded-full border border-amber-500/40 bg-amber-500/10 px-2 py-0.5 font-medium text-amber-300"
            >
              Unsaved changes
            </span>
          {:else if state.status === "ready"}
            <span class="inline-flex items-center gap-1.5 text-muted-foreground">
              <span
                class="h-2 w-2 rounded-full bg-accent shadow-[0_0_8px_1px_rgba(34,197,94,0.55)]"
                aria-hidden="true"
              ></span>
              All changes saved
            </span>
          {/if}
          {#if config !== null}
            <span class="text-muted-foreground">
              {enabledCount} of {CATEGORY_META.length} categories enabled
            </span>
          {/if}
        </div>
      </div>

      <div class="shrink-0">
        <SaveButton
          dirty={isDirty}
          busy={state.busy}
          blockedReason={check.ok ? "" : (check.messages[0] ?? "Fix the highlighted fields.")}
          error={state.error ?? ""}
          onsave={saveConfig}
          onreload={reloadFromDisk}
        />
      </div>
    </header>

    {#if state.status === "loading" && config === null}
      <div class="flex flex-col items-center justify-center gap-3 py-24 text-center">
        <span
          class="h-5 w-5 animate-spin rounded-full border-2 border-muted-foreground/25 border-t-accent"
          aria-hidden="true"
        ></span>
        <p class="text-sm text-muted-foreground">Loading configuration…</p>
      </div>
    {:else if config === null}
      <div
        class="mt-8 rounded-lg border border-destructive/40 bg-destructive/5 p-6 text-sm"
        role="alert"
      >
        <h2 class="font-semibold text-rose-300">Could not load configuration</h2>
        <p class="mt-2 text-muted-foreground">
          {state.error ?? "The configuration API did not return a document."}
        </p>
        <p class="mt-2 text-xs text-muted-foreground/90">
          The editor calls <code class="font-mono">/api/config</code>, which the
          proxy serves when the rules API is enabled.
        </p>
        <Button type="button" variant="outline" size="sm" class="mt-4" onclick={() => void loadConfig()}>
          Try again
        </Button>
      </div>
    {:else}
      <div class="mt-5 space-y-4">
        {#if state.error !== null}
          <div
            class="flex items-start gap-3 rounded-lg border border-destructive/40 bg-destructive/5 p-3 text-sm"
            role="alert"
          >
            <svg
              class="mt-0.5 h-4 w-4 shrink-0 text-destructive"
              viewBox="0 0 16 16"
              aria-hidden="true"
              focusable="false"
            >
              <path
                d="M8 1.5a6.5 6.5 0 1 0 0 13 6.5 6.5 0 0 0 0-13Zm0 2.25a.75.75 0 0 1 .75.75v3.5a.75.75 0 0 1-1.5 0V4.5A.75.75 0 0 1 8 3.75Zm0 7.5a.875.875 0 1 1 0 1.75.875.875 0 0 1 0-1.75Z"
                fill="currentColor"
              />
            </svg>
            <div class="min-w-0 flex-1">
              <p class="font-medium text-rose-300">Request failed</p>
              <p class="mt-1 break-words text-muted-foreground">{state.error}</p>
              {#if state.field !== ""}
                <p class="mt-1 font-mono text-[11px] text-muted-foreground/90">
                  {state.field}
                </p>
              {/if}
            </div>
            <button
              type="button"
              class="shrink-0 rounded-md px-2 py-0.5 text-xs text-muted-foreground transition-colors hover:bg-secondary/60 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/60"
              onclick={dismissError}
            >
              Dismiss
            </button>
          </div>
        {/if}

        {#if state.notice !== null}
          <div
            class="flex items-start gap-3 rounded-lg border border-accent/40 bg-accent/10 p-3 text-sm"
            role="status"
            aria-live="polite"
          >
            <span
              class="mt-1.5 h-2 w-2 shrink-0 rounded-full bg-accent shadow-[0_0_8px_1px_rgba(34,197,94,0.5)]"
              aria-hidden="true"
            ></span>
            <p class="min-w-0 flex-1 text-emerald-200">{state.notice}</p>
            <button
              type="button"
              class="shrink-0 rounded-md px-2 py-0.5 text-xs text-muted-foreground transition-colors hover:bg-secondary/60 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/60"
              onclick={dismissNotice}
            >
              Dismiss
            </button>
          </div>
        {/if}

        {#if !check.ok}
          <div
            class="rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 text-sm"
            role="alert"
          >
            <p class="font-medium text-amber-200">
              Fix these before saving
            </p>
            <ul class="mt-1 list-inside list-disc space-y-0.5 text-xs text-muted-foreground">
              {#each check.messages as message (message)}
                <li>{message}</li>
              {/each}
            </ul>
          </div>
        {/if}

        {#if logChanged}
          <div
            class="flex items-start gap-3 rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 text-sm"
            role="status"
          >
            <svg
              class="mt-0.5 h-4 w-4 shrink-0 text-amber-400"
              viewBox="0 0 16 16"
              aria-hidden="true"
              focusable="false"
            >
              <path
                d="M8.22 1.75a.75.75 0 0 0-1.44 0l-5 12A.75.75 0 0 0 2.5 15h11a.75.75 0 0 0 .72-1.25l-5-12ZM8 4.9l3.5 8.35h-7L8 4.9Zm0 2.6a.75.75 0 0 1 .75.75v2a.75.75 0 0 1-1.5 0v-2A.75.75 0 0 1 8 7.5Zm0 4.25a.875.875 0 1 1 0 1.75.875.875 0 0 1 0-1.75Z"
                fill="currentColor"
              />
            </svg>
            <div class="min-w-0">
              <p class="font-medium text-amber-200">
                Logging settings changed
              </p>
              <p class="mt-0.5 text-xs text-muted-foreground">
                These changes apply immediately and affect all future requests.
              </p>
            </div>
          </div>
        {/if}

        <div class="grid gap-5 lg:grid-cols-[minmax(0,1fr)_minmax(0,25rem)]">
          <div class="min-w-0 space-y-5">
            <section
              class="rounded-lg border border-border/80 bg-card/40"
              aria-labelledby="logging-heading"
            >
              <div class="border-b border-border/60 px-4 py-3">
                <h2 id="logging-heading" class="text-sm font-semibold">Logging</h2>
                <p class="mt-0.5 text-xs text-muted-foreground">
                  Diagnostics written to the proxy log at info level.
                </p>
              </div>
              <div class="grid gap-2 p-3 sm:grid-cols-2 sm:p-4">
                {@render logToggle(
                  "log_redacted",
                  "Log redacted values",
                  "Emit each placeholder, category, and original value.",
                )}
                {@render logToggle(
                  "log_bodies",
                  "Log request and response bodies",
                  "Log full bodies for every proxied request. Verbose.",
                )}
              </div>
            </section>

            <section class="space-y-3" aria-labelledby="categories-heading">
              <div>
                <h2 id="categories-heading" class="text-sm font-semibold">
                  Redaction categories
                </h2>
                <p class="mt-0.5 text-xs text-muted-foreground">
                  Disabled categories pass through untouched. Exact values and
                  patterns extend the built-in detector; denylisted values are
                  never redacted.
                </p>
              </div>

              <div class="space-y-3">
                {#each CATEGORY_META as meta (meta.key)}
                  {@const category = config.pii[meta.key]}
                  <CategoryEditor
                    {meta}
                    {category}
                    enabled={categoryEnabled(config.pii, meta)}
                    error={categoryError(meta.key)}
                    disabled={state.busy}
                    onenabledchange={(enabled) => setCategoryEnabled(meta.key, enabled)}
                    onmodechange={(mode) => setCategoryMode(meta.key, mode)}
                    ontextchange={(text) => setCategoryText(meta.key, text)}
                    onlistchange={(field, items) => handleList(meta.key, field, items)}
                  />
                {/each}
              </div>
            </section>
          </div>

          <aside class="min-w-0 lg:sticky lg:top-5 lg:self-start">
            <LivePreview {config} />
          </aside>
        </div>
      </div>
    {/if}
  </div>
</div>

{#snippet logToggle(flag: LogFlagKey, title: string, description: string)}
  <button
    type="button"
    role="switch"
    aria-checked={logValue(flag)}
    onclick={() => setLogFlag(flag, !logValue(flag))}
    disabled={state.busy}
    class="flex w-full items-start gap-3 rounded-md border border-border/70 bg-background/40 p-3 text-left transition-colors hover:border-border hover:bg-secondary/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/50 disabled:pointer-events-none disabled:opacity-50"
  >
    <span class="min-w-0 flex-1">
      <span class="block text-sm font-medium">{title}</span>
      <span class="mt-0.5 block text-xs text-muted-foreground">{description}</span>
    </span>
    <span
      class="relative mt-0.5 inline-flex h-5 w-9 shrink-0 items-center rounded-full border border-transparent transition-colors {logValue(
        flag,
      )
        ? 'bg-accent shadow-[0_0_10px_-2px_rgba(34,197,94,0.8)]'
        : 'bg-muted'}"
      aria-hidden="true"
    >
      <span
        class="pointer-events-none block h-4 w-4 rounded-full bg-white shadow transition-transform {logValue(
          flag,
        )
          ? 'translate-x-4'
          : 'translate-x-0.5'}"
      ></span>
    </span>
  </button>
{/snippet}
