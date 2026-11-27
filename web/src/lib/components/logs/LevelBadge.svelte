<script lang="ts">
  import type { LogLevel } from "$lib/stores/logs";
  import { cn } from "$lib/utils.js";

  interface Props {
    level: LogLevel;
    class?: string;
  }

  let { level, class: className }: Props = $props();

  const styles: Record<LogLevel, string> = {
    debug: "border-sky-400/30 bg-sky-400/10 text-sky-300",
    info: "border-emerald-400/30 bg-emerald-400/10 text-emerald-300",
    warn: "border-amber-400/30 bg-amber-400/10 text-amber-200",
    error: "border-rose-400/30 bg-rose-400/10 text-rose-300",
  };
</script>

<span
  class={cn(
    "inline-flex h-5 min-w-14 items-center justify-center rounded border px-1.5 font-mono text-[10px] font-semibold uppercase tracking-wide",
    styles[level],
    className,
  )}
>
  {level}
</span>
