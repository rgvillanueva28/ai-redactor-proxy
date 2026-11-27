<script lang="ts">
  import type { LogLevel } from "$lib/stores/logs";
  import { cn } from "$lib/utils.js";

  interface Props {
    level: LogLevel;
    class?: string;
  }

  let { level, class: className }: Props = $props();

  const styles: Record<LogLevel, string> = {
    debug: "border-sky-500/40 bg-sky-500/10 text-sky-300",
    info: "border-emerald-500/40 bg-emerald-500/10 text-emerald-300",
    warn: "border-amber-500/40 bg-amber-500/10 text-amber-300",
    error: "border-rose-500/40 bg-rose-500/10 text-rose-300",
  };
</script>

<span
  class={cn(
    "inline-flex h-5 min-w-14 items-center justify-center rounded border px-1.5 text-[10px] font-semibold uppercase tracking-wide",
    styles[level],
    className,
  )}
>
  {level}
</span>
