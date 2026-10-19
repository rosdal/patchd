import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [tanstackRouter({ target: "react", autoCodeSplitting: true }), react()],
  build: { outDir: "dist" }, // embedded into the Go binary by api/server/static.go
  server: {
    proxy: { "/patchd.v1.": "http://localhost:8080" },
  },
});
