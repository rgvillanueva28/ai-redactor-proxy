<script lang="ts">
  import { onMount } from "svelte";
  import { replace } from "svelte-spa-router";

  // The same bundle is mounted at both /logs and /config. When no hash route is
  // present, pick the view that matches the mount prefix the page was opened
  // from, then let the router take over.
  function defaultRoute(): "/logs" | "/config" {
    const path = window.location.pathname.replace(/\/+$/, "");
    return path.endsWith("/config") ? "/config" : "/logs";
  }

  onMount(() => {
    void replace(defaultRoute());
  });
</script>

<div class="flex h-full items-center justify-center text-sm text-muted-foreground">
  Loading…
</div>
