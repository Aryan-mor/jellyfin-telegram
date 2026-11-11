import path from "node:path"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vite"

const root = import.meta.dirname
const api = "http://127.0.0.1:8097"

// /setup and /panel are API prefixes. The dev pages are /setup.html and /panel.html,
// and a prefix proxy would send those HTML files to the running server.
function apiProxy(page: string) {
  return {
    target: api,
    bypass(req: { url?: string }) {
      const path = (req.url ?? "").split("?")[0]
      if (path === page) return path
    },
  }
}

export default defineConfig({
  base: "/ui/",
  plugins: [react(), tailwindcss()],
  resolve: { alias: { "@": path.resolve(root, "src") } },
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
    rollupOptions: {
      input: {
        panel: path.resolve(root, "panel.html"),
        setup: path.resolve(root, "setup.html"),
      },
    },
  },
  server: {
    proxy: {
      "/panel": apiProxy("/panel.html"),
      "/setup": apiProxy("/setup.html"),
    },
  },
})
