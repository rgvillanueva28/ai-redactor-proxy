<script lang="ts">
  import { Button } from "$lib/components/ui/button/index.js";
  import { cn } from "$lib/utils.js";
  import type { CategoryConfig, ReplacementMode } from "$lib/api/config";
  import {
    categoryList,
    categoryMode,
    categoryText,
    type CategoryListField,
    type CategoryMeta,
  } from "$lib/stores/config";
  import ReplacementModeSelect from "./ReplacementModeSelect.svelte";

  interface Props {
    meta: CategoryMeta;
    category: CategoryConfig;
    enabled: boolean;
    /** Inline validation or server error for this category. */
    error?: string;
    disabled?: boolean;
    onenabledchange: (enabled: boolean) => void;
    onmodechange: (mode: ReplacementMode) => void;
    ontextchange: (text: string) => void;
    onlistchange: (field: CategoryListField, items: string[]) => void;
  }

  let {
    meta,
    category,
    enabled,
    error = "",
    disabled = false,
    onenabledchange,
    onmodechange,
    ontextchange,
    onlistchange,
  }: Props = $props();

  const mode = $derived(categoryMode(category));
  const text = $derived(categoryText(category));

  // One add-field draft per list. Values are only appended on Add or Enter, so
  // typing does not dirty the form until the operator commits the entry.
  let drafts = $state<Record<CategoryListField, string>>({
    names: "",
    patterns: "",
    denylist: "",
  });

  function changeItem(field: CategoryListField, index: number, value: string): void {
    const items = [...categoryList(category, field)];
    items[index] = value;
    onlistchange(field, items);
  }

  function removeItem(field: CategoryListField, index: number): void {
    const items = [...categoryList(category, field)];
    items.splice(index, 1);
    onlistchange(field, items);
  }

  function addItem(field: CategoryListField): void {
    const value = drafts[field].trim();
    if (value === "") return;

    const items = [...categoryList(category, field)];
    if (items.includes(value)) {
      drafts[field] = "";
      return;
    }
    items.push(value);
    onlistchange(field, items);
    drafts[field] = "";
  }

  function handleDraftKeydown(event: KeyboardEvent, field: CategoryListField): void {
    if (event.key !== "Enter") return;
    event.preventDefault();
    addItem(field);
  }
</script>

<article
  class={cn(
    "rounded-lg border bg-card/40 transition-colors",
    error !== "" ? "border-destructive/60" : "border-border/80",
  )}
  aria-labelledby="category-{meta.key}-title"
>
  <header class="flex items-start gap-3 border-b border-border/60 px-4 py-3">
    <button
      type="button"
      role="switch"
      aria-checked={enabled}
      aria-label="{enabled ? 'Disable' : 'Enable'} {meta.label} redaction"
      onclick={() => onenabledchange(!enabled)}
      {disabled}
      class={cn(
        "relative mt-0.5 inline-flex h-5 w-9 shrink-0 items-center rounded-full border border-transparent transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/60 focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:pointer-events-none disabled:opacity-50",
        enabled
          ? "bg-accent shadow-[0_0_10px_-2px_rgba(34,197,94,0.8)]"
          : "bg-muted",
      )}
    >
      <span
        class={cn(
          "pointer-events-none block h-4 w-4 rounded-full bg-white shadow transition-transform",
          enabled ? "translate-x-4" : "translate-x-0.5",
        )}
      ></span>
    </button>

    <div class="min-w-0 flex-1">
      <h3
        id="category-{meta.key}-title"
        class="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm font-medium"
      >
        <span>{meta.label}</span>
        <code class="rounded bg-muted/60 px-1 py-0.5 font-mono text-[10px] font-normal text-muted-foreground">
          {meta.placeholder}
        </code>
        <span
          class={cn(
            "rounded-full border px-1.5 py-0.5 text-[10px] font-medium",
            enabled
              ? "border-accent/40 bg-accent/10 text-accent"
              : "border-border bg-muted/50 text-muted-foreground",
          )}
        >
          {enabled ? "Enabled" : "Disabled"}
        </span>
      </h3>
      <p class="mt-0.5 text-xs text-muted-foreground">{meta.summary}</p>
    </div>
  </header>

  <div class="space-y-4 px-4 py-4">
    <ReplacementModeSelect
      id={meta.key}
      {mode}
      {text}
      {disabled}
      placeholder={meta.placeholder}
      onmodechange={onmodechange}
      ontextchange={ontextchange}
    />

    <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
      {@render listEditor(
        "names",
        "Exact values",
        "Add a literal value",
        "Matched case-insensitively, for values a pattern cannot express.",
        categoryList(category, "names"),
      )}
      {@render listEditor(
        "patterns",
        "Patterns",
        "Add a Go regular expression",
        "Validated by the proxy when you save.",
        categoryList(category, "patterns"),
      )}
      {@render listEditor(
        "denylist",
        "Never redact",
        "Add an allowed value",
        "Exact values excluded from redaction, including built-in matches.",
        categoryList(category, "denylist"),
      )}
    </div>

    {#if error !== ""}
      <p role="alert" class="text-xs text-rose-300">{error}</p>
    {/if}
  </div>
</article>

{#snippet listEditor(
  field: CategoryListField,
  title: string,
  addPlaceholder: string,
  hint: string,
  items: string[],
)}
  <div class="min-w-0 space-y-1.5">
    <div class="flex items-baseline justify-between gap-2">
      <span class="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
        {title}
      </span>
      <span class="rounded-full bg-muted/60 px-1.5 text-[10px] tabular-nums text-muted-foreground">
        {items.length}
      </span>
    </div>

    {#if items.length === 0}
      <p class="text-[11px] text-muted-foreground/90">None</p>
    {:else}
      <ul class="space-y-1">
        {#each items as item, index (index)}
          <li class="flex items-center gap-1">
            <input
              value={item}
              oninput={(event) => changeItem(field, index, event.currentTarget.value)}
              aria-label="{title} value {index + 1}"
              spellcheck="false"
              autocomplete="off"
              {disabled}
              class="h-8 min-w-0 flex-1 rounded-md border border-border bg-background/70 px-2 font-mono text-[11px] text-foreground focus-visible:border-accent/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/50 disabled:pointer-events-none disabled:opacity-50"
            />
            <button
              type="button"
              class="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50"
              aria-label="Remove {title} value {index + 1}"
              onclick={() => removeItem(field, index)}
              {disabled}
            >
              <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" focusable="false">
                <path
                  d="M3.72 3.72a.75.75 0 0 1 1.06 0L8 6.94l3.22-3.22a.749.749 0 0 1 1.275.326.749.749 0 0 1-.215.734L9.06 8l3.22 3.22a.749.749 0 0 1-.326 1.275.749.749 0 0 1-.734-.215L8 9.06l-3.22 3.22a.751.751 0 0 1-1.042-.018.751.751 0 0 1-.018-1.042L6.94 8 3.72 4.78a.75.75 0 0 1 0-1.06Z"
                  fill="currentColor"
                />
              </svg>
            </button>
          </li>
        {/each}
      </ul>
    {/if}

    <div class="flex items-center gap-1">
      <input
        value={drafts[field]}
        oninput={(event) => (drafts[field] = event.currentTarget.value)}
        onkeydown={(event) => handleDraftKeydown(event, field)}
        placeholder={addPlaceholder}
        aria-label="Add {title.toLowerCase()}"
        spellcheck="false"
        autocomplete="off"
        {disabled}
        class="h-8 min-w-0 flex-1 rounded-md border border-dashed border-border bg-transparent px-2 text-[11px] text-foreground placeholder:text-muted-foreground focus-visible:border-accent/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 disabled:pointer-events-none disabled:opacity-50"
      />
      <Button
        type="button"
        variant="outline"
        size="sm"
        class="h-8 shrink-0 px-2 text-[11px]"
        onclick={() => addItem(field)}
        disabled={disabled || drafts[field].trim() === ""}
      >
        Add
      </Button>
    </div>
    <p class="text-[10px] leading-snug text-muted-foreground/90">{hint}</p>
  </div>
{/snippet}
