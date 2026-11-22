import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  // Recharts alone is about 500 kB: one local page, loaded from the loopback interface, so no code splitting.
  build: { outDir: "dist", emptyOutDir: true, sourcemap: false, chunkSizeWarningLimit: 800 },
  server: {
    // `npm run dev` against `agent-stats serve`. changeOrigin sends the Host the server accepts (127.0.0.1:7373).
    proxy: { "/api": { target: "http://127.0.0.1:7373", changeOrigin: true } },
  },
});
