import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";
import { buildVersion } from "../tools/scripts/version.mjs";

export default defineConfig({
  plugins: [tanstackRouter({ target: "react", autoCodeSplitting: true }), react()],
  define: { __PATCHD_VERSION__: JSON.stringify(buildVersion()) },
  build: { outDir: "dist" }, // embedded into the Go binary by api/server/static.go
  server: {
    proxy: { "/patchd.v1.": "http://localhost:8080" },
  },
  test: { environment: "jsdom" },
});
