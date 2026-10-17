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
      input: { login: here("src/login.css"), setup: here("src/setup.html"), app: here("src/app.html") },
      output: {
        entryFileNames: "[name].js",
        chunkFileNames: "assets/[name]-[hash].js",
        assetFileNames: (info) =>
          // login.css is its own entry; the sheet shared by the setup page and the main
          // interface has a fixed name too, whatever chunk it is taken from.
          (info.names ?? []).includes("login.css") ? "login.css"
            : (info.names ?? []).some((n) => n.endsWith(".css")) ? "app.css" : "assets/[name]-[hash][extname]",
      },
    },
  },
  // npm run dev: the API and i18n.js come from a service running on 8431.
  server: { proxy: { "/login": "http://localhost:8431", "/api": "http://localhost:8431", "/i18n.js": "http://localhost:8431" } },
  test: { root: here("src"), environment: "node" },
});
