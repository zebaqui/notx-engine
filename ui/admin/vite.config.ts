import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/v1": {
        target: "http://localhost:4060",
        changeOrigin: true,
      },
      "/healthz": {
        target: "http://localhost:4060",
        changeOrigin: true,
      },
      "/readyz": {
        target: "http://localhost:4060",
        changeOrigin: true,
      },
    },
  },
});
