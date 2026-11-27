import path from "node:path";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import { defineConfig } from "vite";

// The build output is embedded into the Go binary with go:embed, so the
// directory lives inside the Go package. Filenames are deterministic (no
// content hashes) to keep the embedded tree stable between builds, and the
// base is relative so the same bundle can be served under any mount prefix
// (/logs, /config, ...).
export default defineConfig({
  base: "./",
  plugins: [svelte()],
  resolve: {
    alias: {
      $lib: path.resolve("./src/lib"),
    },
  },
  build: {
    outDir: "../internal/ui/assets",
    emptyOutDir: true,
    rollupOptions: {
      output: {
        entryFileNames: "assets/[name].js",
        chunkFileNames: "assets/[name].js",
        assetFileNames: "assets/[name].[ext]",
      },
    },
  },
});
