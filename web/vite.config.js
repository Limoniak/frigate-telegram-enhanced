// Builds the front end into internal/web/dist, which the Go binary embeds. Entry
// files keep fixed names (the Go side serves them by name); fonts and the code shared
// by the pages (assets/shared-*.js, Svelte's runtime above all) get a hash under assets/.
import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath } from "node:url";

const sheets = new Set(); // source names of the non-login stylesheets of this build
const here = (p) => fileURLToPath(new URL(p, import.meta.url));

export default defineConfig({
  root: here("src"),
  // Vitest's cache lives under web/node_modules, not under src/ (the root above).
  cacheDir: here("node_modules/.vite"),
  base: "./",
  plugins: [tailwindcss(), svelte(), { name: "reset-sheet-count", buildStart() { sheets.clear(); } }],
  build: {
    outDir: here("../internal/web/dist"),
    emptyOutDir: true,
    rollupOptions: {
      input: { login: here("src/login.css"), setup: here("src/setup.html"), app: here("src/app.html") },
      output: {
        entryFileNames: "[name].js",
        // Not [name]: Rollup names a shared chunk after one of its modules (ThemeMenu…).
        chunkFileNames: "assets/shared-[hash].js",
        assetFileNames: (info) => {
          const names = info.names ?? [];
          // login.css is its own entry; the sheet shared by the setup page and the main
          // interface has a fixed name too, whatever chunk it is taken from. A second such
          // sheet (say a <style> block added to a component) would silently replace it, so
          // it fails the build instead.
          if (names.includes("login.css")) return "login.css";
          if (names.some((n) => n.endsWith(".css"))) {
            // Vite asks once per output, so the same sheet can come twice: count names.
            names.forEach((n) => sheets.add(n));
            if (sheets.size > 1) throw new Error("a second stylesheet would overwrite app.css: " + [...sheets].join(", "));
            return "app.css";
          }
          return "assets/[name]-[hash][extname]";
        },
      },
    },
  },
  // npm run dev: the API and i18n.js come from a service running on 8431.
  server: { proxy: { "/login": "http://localhost:8431", "/api": "http://localhost:8431", "/i18n.js": "http://localhost:8431" } },
  test: { root: here("src"), environment: "node" },
});
