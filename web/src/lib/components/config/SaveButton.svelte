<script lang="ts">
  import { tick } from "svelte";
  import { Button, buttonVariants } from "$lib/components/ui/button/index.js";
  import { cn } from "$lib/utils.js";

  interface Props {
    dirty: boolean;
    busy: boolean;
    /** Reason saving is unavailable; empty when the form is valid. */
    blockedReason: string;
    /** Last API error, mirrored inside the confirmation dialog. */
    error: string;
    onsave: () => Promise<boolean>;
    onreload: () => Promise<boolean>;
  }

  let { dirty, busy, blockedReason, error, onsave, onreload }: Props = $props();

  type Intent = "save" | "reload";

  let open = $state(false);
  let intent = $state<Intent>("save");
  let localError = $state("");
  let confirmButton = $state<HTMLButtonElement | null>(null);
  let previousFocus: HTMLElement | null = null;

  const canSave = $derived(dirty && !busy && blockedReason === "");

  // Move focus into the dialog when it opens and restore it on close.
  $effect(() => {
    if (open) {
      previousFocus = document.activeElement as HTMLElement | null;
      confirmButton?.focus();
    } else if (previousFocus !== null && previousFocus.isConnected) {
      previousFocus.focus();
      previousFocus = null;
    }
  });

  // Keep the dialog's inline error in sync with the store. The parent clears
  // it when a retry starts and repopulates it when the request fails.
  $effect(() => {
    if (open && error !== "") localError = error;
  });

  // Lock background scrolling while the modal is up.
  $effect(() => {
    if (!open) return;
    const previous = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = previous;
    };
  });

  function requestSave(): void {
    if (!canSave) return;
    intent = "save";
    localError = "";
    open = true;
  }

  function requestReload(): void {
    if (busy) return;
    intent = "reload";
    localError = "";
    open = true;
  }

  function close(): void {
    if (busy) return;
    open = false;
  }

  function handleKeydown(event: KeyboardEvent): void {
    if (!open || event.key !== "Escape") return;
    event.preventDefault();
    close();
  }

  async function confirm(): Promise<void> {
    localError = "";
    const ok = intent === "save" ? await onsave() : await onreload();
    if (ok) {
      open = false;
      return;
    }

    // The parent writes the failure into the store; wait for it to propagate
    // into this component's `error` prop before mirroring it in the dialog.
    await tick();
    localError =
      error !== ""
        ? error
        : "The request failed. Check the proxy log for details.";
  }
</script>

<svelte:window onkeydown={handleKeydown} />

<div class="flex flex-wrap items-center justify-end gap-2">
  <Button
    type="button"
    variant="outline"
    size="sm"
    class="h-9"
    onclick={requestReload}
    disabled={busy}
    title="Re-read config.toml from disk"
  >
    Reload from disk
  </Button>

  <Button
    type="button"
    size="sm"
    class="h-9"
    onclick={requestSave}
    disabled={!canSave}
    title={blockedReason !== ""
      ? blockedReason
      : dirty
        ? "Save the configuration and reload the proxy"
        : "No unsaved changes"}
  >
    {busy ? "Saving…" : "Save changes"}
  </Button>
</div>

{#if open}
  <div
    class="fixed inset-0 z-50 flex items-center justify-center p-4 sm:p-6"
    role="dialog"
    aria-modal="true"
    aria-labelledby="config-dialog-title"
    aria-describedby="config-dialog-description"
  >
    <button
      type="button"
      class="absolute inset-0 cursor-default bg-background/80 backdrop-blur-sm"
      aria-label="Cancel"
      onclick={close}
    ></button>

    <div
      class="relative w-full max-w-md rounded-lg border border-border/80 bg-card p-5 text-card-foreground shadow-[0_30px_80px_-24px_rgba(2,6,23,0.95)]"
    >
      <h2 id="config-dialog-title" class="text-base font-semibold">
        {intent === "save" ? "Save configuration?" : "Reload configuration?"}
      </h2>
      <p id="config-dialog-description" class="mt-2 text-sm leading-relaxed text-muted-foreground">
        {#if intent === "save"}
          This writes
          <code class="rounded bg-muted/60 px-1 py-0.5 font-mono text-xs text-foreground/80">
            config.toml
          </code>
          and reloads the running proxy. Redaction changes apply to all future
          requests; logging changes take effect immediately.
        {:else if dirty}
          Reloading discards your unsaved changes and re-reads
          <code class="rounded bg-muted/60 px-1 py-0.5 font-mono text-xs text-foreground/80">
            config.toml
          </code>
          from disk.
        {:else}
          Re-read
          <code class="rounded bg-muted/60 px-1 py-0.5 font-mono text-xs text-foreground/80">
            config.toml
          </code>
          from disk and apply it to the running proxy.
        {/if}
      </p>

      {#if localError !== ""}
        <p
          role="alert"
          class="mt-3 rounded-md border border-destructive/40 bg-destructive/10 p-2 text-xs text-rose-300"
        >
          {localError}
        </p>
      {/if}

      <div class="mt-5 flex justify-end gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          class="h-9"
          onclick={close}
          disabled={busy}
        >
          Cancel
        </Button>
        <button
          bind:this={confirmButton}
          type="button"
          class={cn(buttonVariants({ size: "sm" }), "h-9")}
          onclick={confirm}
          disabled={busy}
        >
          {busy ? "Working…" : intent === "save" ? "Save & reload" : "Reload"}
        </button>
      </div>
    </div>
  </div>
{/if}
