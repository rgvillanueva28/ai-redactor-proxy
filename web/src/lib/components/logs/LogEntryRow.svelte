<script lang="ts">
  import type { LogEntry } from "$lib/stores/logs";
  import LevelBadge from "./LevelBadge.svelte";

  interface Props {
    entry: LogEntry;
    onselect: () => void;
  }

  let { entry, onselect }: Props = $props();

  const FieldPreviewLength = 120;

  function previewValue(value: string): string {
    return value.length > FieldPreviewLength
      ? value.slice(0, FieldPreviewLength) + "…"
      : value;
  }
</script>

<button
  type="button"
  class="flex w-full items-start gap-3 px-4 py-2.5 text-left font-mono text-xs transition-colors hover:bg-accent/40 focus-visible:bg-accent/40 focus-visible:outline-none"
  onclick={onselect}
  title="View the full log entry"
>
  <time
    class="w-[76px] shrink-0 pt-0.5 tabular-nums text-muted-foreground"
    datetime={entry.time.iso || undefined}
    title={entry.time.title || undefined}
  >
    {entry.time.display || "—"}
  </time>

  <LevelBadge level={entry.level} class="mt-0.5 shrink-0" />

  <span class="min-w-0 flex-1 space-y-1">
    <span
      class="block whitespace-pre-wrap break-words text-foreground/90"
      class:text-muted-foreground={entry.message === ""}
    >
      {entry.messagePreview || "(no message)"}
    </span>

    {#if entry.messageTruncated}
      <span class="block text-[11px] text-muted-foreground">Click to expand</span>
    {/if}

    {#if entry.fields.length > 0}
      <span class="flex flex-wrap gap-x-3 gap-y-1">
        {#each entry.fields as field (field.key)}
          <span class="inline-flex max-w-full items-baseline gap-1 text-[11px]">
            <span class="text-muted-foreground/80">{field.key}</span>
            <span class="text-muted-foreground/60">=</span>
            <span class="break-all text-foreground/75">{previewValue(field.value)}</span>
          </span>
        {/each}
      </span>
    {/if}
  </span>
</button>
