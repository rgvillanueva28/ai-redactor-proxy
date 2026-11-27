<script lang="ts">
  import { cn } from "$lib/utils.js";
  import type { ReplacementMode } from "$lib/api/config";

  interface Props {
    /** Category key, used to keep the label and input ids unique. */
    id: string;
    mode: ReplacementMode;
    text: string;
    /** Default placeholder prefix, shown in the dropdown and help text. */
    placeholder: string;
    disabled?: boolean;
    onmodechange: (mode: ReplacementMode) => void;
    ontextchange: (text: string) => void;
  }

  let {
    id,
    mode,
    text,
    placeholder,
    disabled = false,
    onmodechange,
    ontextchange,
  }: Props = $props();

  const missingText = $derived(mode === "custom" && text.trim() === "");

  function handleMode(event: Event): void {
    const value = (event.currentTarget as HTMLSelectElement).value;
    if (value === "custom" || value === "placeholder") onmodechange(value);
  }
</script>

<div class="grid gap-3 sm:grid-cols-2">
  <div class="min-w-0 space-y-1">
    <label
      for="replacement-mode-{id}"
      class="block text-[11px] font-semibold uppercase tracking-wide text-muted-foreground"
    >
      Replacement
    </label>
    <select
      id="replacement-mode-{id}"
      value={mode}
      onchange={handleMode}
      {disabled}
      class="h-9 w-full rounded-md border border-input bg-background/60 px-2 text-sm text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50"
    >
      <option value="placeholder">Placeholder ({placeholder})</option>
      <option value="custom">Custom text</option>
    </select>
  </div>

  {#if mode === "custom"}
    <div class="min-w-0 space-y-1">
      <label
        for="replacement-text-{id}"
        class="block text-[11px] font-semibold uppercase tracking-wide text-muted-foreground"
      >
        Custom replacement
      </label>
      <input
        id="replacement-text-{id}"
        value={text}
        oninput={(event) => ontextchange(event.currentTarget.value)}
        placeholder="e.g. [EMAIL]"
        spellcheck="false"
        autocomplete="off"
        {disabled}
        aria-invalid={missingText}
        aria-describedby={missingText ? `replacement-error-${id}` : undefined}
        class={cn(
          "h-9 w-full rounded-md border bg-background/60 px-2 font-mono text-sm text-foreground placeholder:font-sans placeholder:text-muted-foreground/70 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50",
          missingText ? "border-destructive/70" : "border-input",
        )}
      />
      {#if missingText}
        <p id="replacement-error-{id}" class="text-[11px] text-destructive">
          Custom text is required.
        </p>
      {:else}
        <p class="text-[11px] leading-snug text-muted-foreground/70">
          Every match in this category becomes this exact string.
        </p>
      {/if}
    </div>
  {:else}
    <p class="self-end pb-1 text-[11px] leading-snug text-muted-foreground/70">
      Each new value gets a numbered tag, for example
      <code class="rounded bg-muted px-1 py-0.5 font-mono text-foreground/80">
        {placeholder}
      </code>.
    </p>
  {/if}
</div>
