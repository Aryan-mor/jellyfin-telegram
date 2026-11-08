import path from "node:path"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vite"

const root = import.meta.dirname

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
      "/panel": "http://127.0.0.1:8097",
      "/setup": "http://127.0.0.1:8097",
    },
  },
})
