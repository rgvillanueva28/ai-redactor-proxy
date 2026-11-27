<script lang="ts">
  import { onMount } from "svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import {
    connection,
    dropped,
    entries,
    entryLimit,
    logStream,
    reconnectIn,
  } from "$lib/stores/logs";
  import type { ConnectionState, LevelFilter, LogEntry } from "$lib/stores/logs";
  import LogDetailModal from "./LogDetailModal.svelte";
  import LogEntryRow from "./LogEntryRow.svelte";

  const NearBottomPx = 32;
  const SearchDebounceMs = 120;

  const levelFilters: { value: LevelFilter; label: string; dot: string }[] = [
    { value: "all", label: "All", dot: "" },
    { value: "debug", label: "Debug", dot: "bg-sky-400" },
    { value: "info", label: "Info", dot: "bg-emerald-400" },
    { value: "warn", label: "Warn", dot: "bg-amber-400" },
    { value: "error", label: "Error", dot: "bg-rose-400" },
  ];

  const statusLabels: Record<ConnectionState, string> = {
    connected: "Connected",
    connecting: "Connecting…",
    reconnecting: "Reconnecting…",
    disconnected: "Disconnected",
    unsupported: "Live streaming unsupported",
  };

  const statusDots: Record<ConnectionState, string> = {
    connected: "bg-emerald-500",
    connecting: "bg-amber-400 animate-pulse",
    reconnecting: "bg-amber-400 animate-pulse",
    disconnected: "bg-rose-500",
    unsupported: "bg-zinc-500",
  };

  let viewer = $state<HTMLDivElement | null>(null);
  let searchInput = $state<HTMLInputElement | null>(null);

  let autoScroll = $state(true);
  let level = $state<LevelFilter>("all");
  let queryInput = $state("");
  let query = $state("");
  let selected = $state<LogEntry | null>(null);

  let searchTimer: ReturnType<typeof setTimeout> | undefined;

  const filtered = $derived(
    $entries.filter((entry) => {
      if (level !== "all" && entry.level !== level) return false;
      if (query !== "" && !entry.haystack.includes(query)) return false;
      return true;
    }),
  );

  const total = $derived($entries.length);
  const visible = $derived(filtered.length);

  const statsText = $derived(
    total === 0
      ? "No entries"
      : visible === total
        ? `${total.toLocaleString()} ${total === 1 ? "entry" : "entries"} · limit ${entryLimit.toLocaleString()}`
        : `Showing ${visible.toLocaleString()} of ${total.toLocaleString()} entries`,
  );

  const statusText = $derived.by(() => {
    if ($connection === "reconnecting" && $reconnectIn > 0) {
      return `Reconnecting in ${$reconnectIn}s…`;
    }
    return statusLabels[$connection];
  });

  onMount(() => {
    logStream.start();
    return () => logStream.stop();
  });

  // Follow the tail whenever entries (or the visible subset) change, unless the
  // reader has paused auto-scroll.
  $effect(() => {
    const visibleEntries = filtered;
    if (!autoScroll || visibleEntries.length === 0) return;
    stickToBottom();
  });

  function stickToBottom(): void {
    if (!viewer) return;
    viewer.scrollTop = viewer.scrollHeight;
  }

  function isNearBottom(): boolean {
    if (!viewer) return true;
    return viewer.scrollHeight - viewer.scrollTop - viewer.clientHeight <= NearBottomPx;
  }

  function handleScroll(): void {
    if (isNearBottom()) {
      if (!autoScroll) autoScroll = true;
    } else if (autoScroll) {
      autoScroll = false;
    }
  }

  function toggleAutoScroll(): void {
    const next = !autoScroll;
    autoScroll = next;
    if (next) stickToBottom();
  }

  function scheduleSearch(): void {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => {
      query = queryInput.trim().toLowerCase();
    }, SearchDebounceMs);
  }

  function clearSearch(): void {
    clearTimeout(searchTimer);
    queryInput = "";
    query = "";
  }

  function handleSearchKeydown(event: KeyboardEvent): void {
    if (event.key !== "Escape") return;
    event.preventDefault();
    clearSearch();
  }

  function handleWindowKeydown(event: KeyboardEvent): void {
    if (selected !== null) return;
    if (event.key !== "/" || event.ctrlKey || event.metaKey || event.altKey) return;

    const target = event.target as HTMLElement | null;
    if (
      target &&
      (target.isContentEditable ||
        target.tagName === "INPUT" ||
        target.tagName === "TEXTAREA" ||
        target.tagName === "SELECT")
    ) {
      return;
    }

    event.preventDefault();
    searchInput?.focus();
  }

  function clearEntries(): void {
    logStream.clear();
  }

  function filterButtonClass(value: LevelFilter): string {
    return level === value
      ? "border-transparent bg-secondary text-secondary-foreground"
      : "border-border/70 bg-transparent text-muted-foreground hover:bg-accent hover:text-accent-foreground";
  }
</script>

<svelte:window onkeydown={handleWindowKeydown} />

<section class="flex h-full flex-col overflow-hidden" aria-label="Live logs">
  <div class="shrink-0 space-y-3 border-b border-border/60 bg-card/30 px-4 py-3 sm:px-6">
    <div class="flex flex-wrap items-center gap-2">
      <div class="flex flex-wrap items-center gap-1.5" role="group" aria-label="Filter by log level">
        {#each levelFilters as filter (filter.value)}
          <button
            type="button"
            class="inline-flex h-8 items-center gap-1.5 rounded-full border px-3 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring {filterButtonClass(
              filter.value,
            )}"
            aria-pressed={level === filter.value}
            onclick={() => (level = filter.value)}
          >
            {#if filter.dot}
              <span class="h-1.5 w-1.5 rounded-full {filter.dot}" aria-hidden="true"></span>
            {/if}
            {filter.label}
          </button>
        {/each}
      </div>

      <div class="relative ml-auto w-full sm:w-64">
        <svg
          class="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground"
          viewBox="0 0 16 16"
          aria-hidden="true"
          focusable="false"
        >
          <path
            d="M7 1a6 6 0 1 0 3.7 10.7l3.3 3.3 1.4-1.4-3.3-3.3A6 6 0 0 0 7 1Zm0 2a4 4 0 1 1 0 8 4 4 0 0 1 0-8Z"
            fill="currentColor"
          />
        </svg>
        <input
          bind:this={searchInput}
          bind:value={queryInput}
          type="search"
          placeholder="Search logs…"
          autocomplete="off"
          spellcheck="false"
          aria-label="Search logs"
          title="Press / to search"
          class="h-9 w-full rounded-md border border-input bg-background/60 pl-8 pr-3 text-sm text-foreground placeholder:text-muted-foreground/70 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          oninput={scheduleSearch}
          onkeydown={handleSearchKeydown}
        />
      </div>

      <div class="flex items-center gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          class="h-8 text-xs"
          aria-pressed={autoScroll}
          onclick={toggleAutoScroll}
        >
          {autoScroll ? "Pause" : "Resume"}
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          class="h-8 text-xs"
          onclick={clearEntries}
        >
          Clear
        </Button>
      </div>
    </div>

    <div class="flex flex-wrap items-center gap-x-4 gap-y-1.5 text-[11px] text-muted-foreground">
      <span class="inline-flex items-center gap-1.5" role="status" aria-live="polite">
        <span class="h-2 w-2 rounded-full {statusDots[$connection]}" aria-hidden="true"></span>
        <span>{statusText}</span>
      </span>

      <span>{statsText}</span>

      {#if $dropped > 0}
        <span
          class="rounded-full border border-amber-500/40 bg-amber-500/10 px-2 py-0.5 font-medium text-amber-300"
          title="Entries lost to the server's queue or to the viewer's entry cap"
        >
          {$dropped.toLocaleString()} dropped
        </span>
      {/if}

      <span class="ml-auto hidden items-center gap-1 sm:inline-flex">
        Streaming from <code class="font-mono text-muted-foreground/90">/logs/events</code>
      </span>
    </div>
  </div>

  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <div
    bind:this={viewer}
    class="min-h-0 flex-1 overflow-y-auto focus-visible:outline-none"
    role="log"
    aria-live="polite"
    aria-relevant="additions"
    tabindex="0"
    onscroll={handleScroll}
  >
    {#if filtered.length === 0}
      <div class="flex min-h-full flex-col items-center justify-center gap-2 px-6 py-16 text-center">
        <span class="flex h-10 w-10 items-center justify-center rounded-full border border-border/60 bg-muted/30 text-muted-foreground">
          <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false">
            <path
              d="M2 3.5A1.5 1.5 0 0 1 3.5 2h9A1.5 1.5 0 0 1 14 3.5v9a1.5 1.5 0 0 1-1.5 1.5h-9A1.5 1.5 0 0 1 2 12.5v-9ZM3.5 3a.5.5 0 0 0-.5.5v9a.5.5 0 0 0 .5.5h9a.5.5 0 0 0 .5-.5v-9a.5.5 0 0 0-.5-.5h-9Z"
              fill="currentColor"
            />
            <path d="M4.5 5.75h7v1h-7v-1Zm0 2.5h7v1h-7v-1Zm0 2.5h4.5v1H4.5v-1Z" fill="currentColor" />
          </svg>
        </span>
        <p class="text-sm text-muted-foreground">
          {total === 0
            ? "Waiting for log entries…"
            : "No entries match the current filters."}
        </p>
      </div>
    {:else}
      <ul class="divide-y divide-border/50">
        {#each filtered as entry (entry.id)}
          <li>
            <LogEntryRow {entry} onselect={() => (selected = entry)} />
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</section>

{#if selected}
  <LogDetailModal entry={selected} onclose={() => (selected = null)} />
{/if}
