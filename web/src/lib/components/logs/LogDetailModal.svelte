<script lang="ts">
  import { onMount } from "svelte";
  import { buttonVariants } from "$lib/components/ui/button/index.js";
  import { cn } from "$lib/utils.js";
  import type { LogEntry } from "$lib/stores/logs";
  import LevelBadge from "./LevelBadge.svelte";

  interface Props {
    entry: LogEntry;
    onclose: () => void;
  }

  let { entry, onclose }: Props = $props();

  let closeButton = $state<HTMLButtonElement | null>(null);

  onMount(() => {
    const previous = document.activeElement as HTMLElement | null;
    const previousOverflow = document.body.style.overflow;

    document.body.style.overflow = "hidden";
    closeButton?.focus();

    return () => {
      document.body.style.overflow = previousOverflow;
      if (previous?.isConnected) previous.focus();
    };
  });

  function handleKeydown(event: KeyboardEvent): void {
    if (event.key !== "Escape") return;
    event.preventDefault();
    onclose();
  }
</script>

<svelte:window onkeydown={handleKeydown} />

<div
  class="fixed inset-0 z-50 flex items-center justify-center p-4 sm:p-6"
  role="dialog"
  aria-modal="true"
  aria-labelledby="log-detail-title"
>
  <button
    type="button"
    class="absolute inset-0 cursor-default bg-black/60 backdrop-blur-sm"
    aria-label="Close log entry details"
    onclick={onclose}
  ></button>

  <div
    class="relative flex max-h-[85vh] w-full max-w-3xl flex-col overflow-hidden rounded-lg border border-border bg-card text-card-foreground shadow-2xl"
  >
    <header class="flex shrink-0 items-center gap-3 border-b border-border px-5 py-3">
      <h2 id="log-detail-title" class="text-sm font-semibold">Log entry</h2>
      <LevelBadge level={entry.level} />
      <button
        bind:this={closeButton}
        type="button"
        class={cn(buttonVariants({ variant: "ghost", size: "icon" }), "ml-auto h-8 w-8")}
        aria-label="Close"
        onclick={onclose}
      >
        <svg
          viewBox="0 0 16 16"
          width="16"
          height="16"
          aria-hidden="true"
          focusable="false"
        >
          <path
            d="M3.72 3.72a.75.75 0 0 1 1.06 0L8 6.94l3.22-3.22a.749.749 0 0 1 1.275.326.749.749 0 0 1-.215.734L9.06 8l3.22 3.22a.749.749 0 0 1-.326 1.275.749.749 0 0 1-.734-.215L8 9.06l-3.22 3.22a.751.751 0 0 1-1.042-.018.751.751 0 0 1-.018-1.042L6.94 8 3.72 4.78a.75.75 0 0 1 0-1.06Z"
            fill="currentColor"
          />
        </svg>
      </button>
    </header>

    <div class="min-h-0 flex-1 space-y-5 overflow-y-auto px-5 py-4">
      <div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
        <span class="font-mono">
          {entry.time.title || entry.time.display || "(no timestamp)"}
        </span>
        {#if entry.time.iso}
          <span class="font-mono text-muted-foreground/70">{entry.time.iso}</span>
        {/if}
      </div>

      <section class="space-y-2">
        <h3 class="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
          Message
        </h3>
        <pre
          class="whitespace-pre-wrap break-words rounded-md border border-border/60 bg-muted/30 p-3 font-mono text-xs text-foreground/90"
        >{entry.message || "(no message)"}</pre>
      </section>

      {#if entry.fields.length > 0}
        <section class="space-y-2">
          <h3 class="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
            Fields
          </h3>
          <dl class="grid gap-2">
            {#each entry.fields as field (field.key)}
              <div
                class="grid grid-cols-1 gap-1 rounded-md border border-border/50 bg-muted/20 p-2 sm:grid-cols-[minmax(0,14rem)_1fr]"
              >
                <dt class="break-all font-mono text-[11px] text-muted-foreground">
                  {field.key}
                </dt>
                <dd
                  class="whitespace-pre-wrap break-words font-mono text-xs text-foreground/90"
                >
                  {field.value}
                </dd>
              </div>
            {/each}
          </dl>
        </section>
      {/if}
    </div>
  </div>
</div>
