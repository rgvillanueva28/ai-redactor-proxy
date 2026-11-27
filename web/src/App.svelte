<script lang="ts">
  import Router from "svelte-spa-router";
  import { link, router } from "svelte-spa-router";
  import type { RouteDefinition } from "svelte-spa-router";
  import ConfigView from "$lib/components/config/ConfigView.svelte";
  import IndexRedirect from "$lib/components/IndexRedirect.svelte";
  import LogsView from "$lib/components/logs/LogsView.svelte";

  // Hash routes. The Go server mounts this bundle at /logs and /config and the
  // router owns everything after the "#".
  const routes: RouteDefinition = {
    "/logs": LogsView,
    "/config": ConfigView,
    "/": IndexRedirect,
    "*": IndexRedirect,
  };

  function tabClass(path: "/logs" | "/config"): string {
    return router.location === path
      ? "bg-accent text-accent-foreground"
      : "text-muted-foreground hover:bg-accent/60 hover:text-accent-foreground";
  }
</script>

<div class="flex h-screen flex-col bg-background text-foreground">
  <header class="shrink-0 border-b border-border/60 bg-card/40 backdrop-blur">
    <div class="mx-auto flex h-14 w-full max-w-7xl items-center gap-4 px-4 sm:px-6">
      <a
        href="/logs"
        use:link
        class="flex items-center gap-2.5 rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <span
          class="flex h-7 w-7 items-center justify-center rounded-md bg-gradient-to-br from-primary to-primary/60 text-[11px] font-bold text-primary-foreground shadow-sm"
          aria-hidden="true"
        >
          R
        </span>
        <span class="flex flex-col leading-tight">
          <span class="text-sm font-semibold tracking-tight">Redactor</span>
          <span class="hidden text-[10px] uppercase tracking-widest text-muted-foreground sm:block">
            Local proxy
          </span>
        </span>
      </a>

      <nav class="flex items-center gap-1" aria-label="Primary">
        <a
          href="/logs"
          use:link
          class="inline-flex h-8 items-center rounded-md px-3 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring {tabClass(
            '/logs',
          )}"
          aria-current={router.location === "/logs" ? "page" : undefined}
        >
          Logs
        </a>
        <a
          href="/config"
          use:link
          class="inline-flex h-8 items-center rounded-md px-3 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring {tabClass(
            '/config',
          )}"
          aria-current={router.location === "/config" ? "page" : undefined}
        >
          Config
        </a>
      </nav>

      <span class="ml-auto hidden text-[11px] text-muted-foreground md:block">
        Everything stays on this machine
      </span>
    </div>
  </header>

  <main class="min-h-0 flex-1">
    <Router {routes} />
  </main>
</div>
