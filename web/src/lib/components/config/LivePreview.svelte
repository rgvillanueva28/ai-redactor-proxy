<script lang="ts">
  import { onDestroy } from "svelte";
  import {
    errorMessage,
    fetchPreview,
    isAbortError,
    type AppConfig,
    type PreviewResponse,
  } from "$lib/api/config";

  interface Props {
    /** Current editor state; previews run before it is saved. */
    config: AppConfig;
  }

  let { config }: Props = $props();

  const DebounceMs = 300;
  const MaxMatches = 40;

  const DefaultSample = `Hi, this is Jane Doe from Acme Corp.
Email jane.doe@example.com or call +1 (555) 123-4567.
Card 4242 4242 4242 4242 · SSN 123-45-6789 · IP 192.168.1.42
Key sk-proj-abc123def456`;

  const categoryLabels: Record<string, string> = {
    EMAIL: "Email",
    PHONE: "Phone",
    CC: "Credit card",
    SSN: "SSN",
    IP: "IP",
    PATH: "Path",
    APIKEY: "API key",
    CRED: "Credential",
    DOMAIN: "Domain",
    CLIENT: "Client",
    NAME: "Name",
  };

  let sample = $state(DefaultSample);
  let result = $state<PreviewResponse | null>(null);
  let error = $state("");
  let pending = $state(false);
  let sequence = 0;
  let controller: AbortController | null = null;

  const count = $derived(result?.matches.length ?? 0);
  const visibleMatches = $derived((result?.matches ?? []).slice(0, MaxMatches));
  const hiddenMatches = $derived(Math.max(0, count - MaxMatches));

  $effect(() => {
    const current = config;
    const text = sample;
    const id = ++sequence;

    // Supersede any request started for the previous text or config.
    controller?.abort();
    controller = null;

    if (text.trim() === "") {
      result = null;
      error = "";
      pending = false;
      return;
    }

    pending = true;
    const timer = setTimeout(() => {
      void run(id, text, current);
    }, DebounceMs);
    return () => clearTimeout(timer);
  });

  async function run(id: number, text: string, cfg: AppConfig): Promise<void> {
    const local = new AbortController();
    controller = local;

    try {
      const response = await fetchPreview(text, cfg, local.signal);
      if (id !== sequence) return;
      result = response;
      error = "";
    } catch (failure) {
      if (isAbortError(failure) || id !== sequence) return;
      result = null;
      error = errorMessage(failure);
    } finally {
      if (id === sequence) pending = false;
      if (controller === local) controller = null;
    }
  }

  onDestroy(() => controller?.abort());

  function categoryLabel(category: string): string {
    return categoryLabels[category] ?? category;
  }

  function badgeClass(category: string): string {
    switch (category) {
      case "EMAIL":
        return "border-sky-400/30 bg-sky-400/10 text-sky-300";
      case "PHONE":
        return "border-emerald-400/30 bg-emerald-400/10 text-emerald-300";
      case "CC":
        return "border-amber-400/30 bg-amber-400/10 text-amber-200";
      case "SSN":
        return "border-rose-400/30 bg-rose-400/10 text-rose-300";
      case "IP":
        return "border-violet-400/30 bg-violet-400/10 text-violet-300";
      case "PATH":
        return "border-cyan-400/30 bg-cyan-400/10 text-cyan-300";
      case "APIKEY":
        return "border-orange-400/30 bg-orange-400/10 text-orange-300";
      case "CRED":
        return "border-red-400/30 bg-red-400/10 text-red-300";
      case "DOMAIN":
        return "border-indigo-400/30 bg-indigo-400/10 text-indigo-300";
      case "CLIENT":
        return "border-teal-400/30 bg-teal-400/10 text-teal-300";
      case "NAME":
        return "border-fuchsia-400/30 bg-fuchsia-400/10 text-fuchsia-300";
      default:
        return "border-border bg-muted/40 text-muted-foreground";
    }
  }
</script>

<section
  class="rounded-lg border border-border/80 bg-card/40"
  aria-labelledby="live-preview-heading"
>
  <header class="flex items-center gap-2 border-b border-border/60 px-4 py-3">
    <h2 id="live-preview-heading" class="text-sm font-semibold">Live preview</h2>
    {#if pending}
      <span
        class="ml-auto inline-flex items-center gap-1.5 text-[11px] text-muted-foreground"
        role="status"
        aria-live="polite"
      >
        <span class="h-2 w-2 animate-pulse rounded-full bg-amber-500" aria-hidden="true"></span>
        Previewing…
      </span>
    {:else if result !== null}
      <span class="ml-auto text-[11px] text-muted-foreground">
        {count} {count === 1 ? "match" : "matches"}
      </span>
    {/if}
  </header>

  <div class="space-y-3 p-4">
    <div class="space-y-1">
      <label
        for="preview-sample"
        class="block text-[11px] font-semibold uppercase tracking-wide text-muted-foreground"
      >
        Sample text
      </label>
      <textarea
        id="preview-sample"
        bind:value={sample}
        rows="5"
        spellcheck="false"
        placeholder="Paste text to see how it is redacted…"
        class="w-full resize-y rounded-md border border-border bg-background/70 p-2 font-mono text-xs leading-relaxed text-foreground placeholder:font-sans placeholder:text-muted-foreground focus-visible:border-accent/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/50"
      ></textarea>
      <p class="text-[11px] text-muted-foreground/90">
        Runs against the unsaved editor state.
      </p>
    </div>

    <div class="space-y-1">
      <span class="block text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
        Redacted output
      </span>
      {#if error !== ""}
        <p
          role="status"
          class="rounded-md border border-destructive/40 bg-destructive/10 p-2 text-xs text-rose-300"
        >
          {error}
        </p>
      {:else}
        <pre
          class="min-h-20 max-h-72 overflow-auto whitespace-pre-wrap break-words rounded-md border border-border/70 bg-background/60 p-2 font-mono text-xs leading-relaxed text-foreground/90"
          aria-live="polite">{result?.redacted ?? "Preview appears here."}</pre>
      {/if}
    </div>

    {#if result !== null && result.matches.length > 0}
      <div class="space-y-1.5">
        <span class="block text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
          Matches
        </span>
        <ul class="max-h-56 space-y-1 overflow-y-auto pr-1">
          {#each visibleMatches as match, index (index)}
            <li
              class="flex flex-wrap items-center gap-x-2 gap-y-1 rounded border border-border/60 bg-background/50 px-2 py-1 text-[11px]"
            >
              <span class="rounded-full border px-1.5 py-0.5 font-medium {badgeClass(match.category)}">
                {categoryLabel(match.category)}
              </span>
              <span class="font-mono tabular-nums text-muted-foreground">
                {match.start}–{match.end}
              </span>
              <span class="ml-auto max-w-full truncate font-mono text-foreground/80">
                {match.replacement}
              </span>
            </li>
          {/each}
        </ul>
        {#if hiddenMatches > 0}
          <p class="text-[11px] text-muted-foreground">…and {hiddenMatches} more.</p>
        {/if}
      </div>
    {/if}
  </div>
</section>
