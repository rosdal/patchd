import { execFileSync } from "node:child_process";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

function gitVersion(): string {
  try {
    return execFileSync("git", ["describe", "--tags", "--always", "--dirty"], {
      encoding: "utf8",
    }).trim();
  } catch {
    return "v0.0.0-dev";
  }
}

export default defineConfig({
  plugins: [tanstackRouter({ target: "react", autoCodeSplitting: true }), react()],
  define: { __PATCHD_VERSION__: JSON.stringify(gitVersion()) },
  build: { outDir: "dist" }, // embedded into the Go binary by api/server/static.go
  server: {
    proxy: { "/patchd.v1.": "http://localhost:8080" },
  },
  test: { environment: "jsdom" },
});
