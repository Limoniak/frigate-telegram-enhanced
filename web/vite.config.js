// Builds the front end into internal/web/dist, which the Go binary embeds. Entry
// files keep fixed names (the Go side serves them by name); fonts and shared
// chunks get a hash under assets/.
import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath } from "node:url";

const here = (p) => fileURLToPath(new URL(p, import.meta.url));

export default defineConfig({
  root: here("src"),
  // Vitest's cache lives under web/node_modules, not under src/ (the root above).
  cacheDir: here("node_modules/.vite"),
  base: "./",
  plugins: [tailwindcss(), svelte()],
  build: {
    outDir: here("../internal/web/dist"),
    emptyOutDir: true,
    rollupOptions: {
      input: { login: here("src/login.css"), setup: here("src/setup.html") },
      output: {
        entryFileNames: "[name].js",
        chunkFileNames: "assets/[name]-[hash].js",
        assetFileNames: (info) =>
          (info.names ?? []).some((n) => n.endsWith(".css")) ? "[name][extname]" : "assets/[name]-[hash][extname]",
      },
    },
  },
  // npm run dev: the API and i18n.js come from a service running on 8431.
  server: { proxy: { "/login": "http://localhost:8431", "/api": "http://localhost:8431", "/i18n.js": "http://localhost:8431" } },
  test: { root: here("src"), environment: "node" },
});
