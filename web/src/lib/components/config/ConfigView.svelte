<script lang="ts">
  // Placeholder for the configuration editor implemented in a later phase.
  // Keeping the route mounted now means navigation between /logs and /config
  // already works in the shipped shell.
</script>

<div class="h-full overflow-y-auto">
  <div class="mx-auto w-full max-w-3xl px-4 py-10 sm:px-6">
    <h1 class="text-xl font-semibold tracking-tight">Configuration</h1>
    <p class="mt-1 text-sm text-muted-foreground">
      Inspect and edit the proxy configuration from the browser.
    </p>

    <div
      class="mt-6 rounded-lg border border-dashed border-border bg-card/30 p-6 text-sm text-muted-foreground"
    >
      The configuration editor is not wired up yet. Until then, edit
      <code class="rounded bg-muted px-1 py-0.5 font-mono text-xs text-foreground/80">
        config.toml
      </code>
      and restart the proxy.
    </div>
  </div>
</div>
