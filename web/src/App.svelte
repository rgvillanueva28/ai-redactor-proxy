<script lang="ts">
  import { Button } from "$lib/components/ui/button/index.js";
</script>

<main class="flex min-h-screen flex-col items-center justify-center gap-6 p-8">
  <div class="space-y-2 text-center">
    <h1 class="text-3xl font-semibold tracking-tight">Redactor</h1>
    <p class="max-w-md text-sm text-muted-foreground">
      The log viewer (<code class="font-mono">/logs</code>) and configuration
      editor (<code class="font-mono">/config</code>) routes will be available
      here.
    </p>
  </div>
  <Button variant="outline" size="sm">Build pipeline ready</Button>
</main>
