import { fileURLToPath, URL } from "node:url";
import { defineConfig } from "vitest/config";

// Dedicated Vitest config. The @vitejs/plugin-react transform breaks hooks in
// test files ("Invalid hook call" / null React exports), so it is omitted here;
// Vite's esbuild handles JSX with the automatic runtime by default.
export default defineConfig({
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: "./src/test/setup.ts",
    css: false,
    // The full-wizard Import tests drive many userEvent steps and run slower
    // under v8 coverage instrumentation; the 5s default is too tight.
    testTimeout: 15000,
    hookTimeout: 15000,
    coverage: {
      provider: "v8",
      reporter: ["text-summary", "lcov"],
      reportsDirectory: "./coverage",
      include: ["src/**/*.{ts,tsx}"],
      exclude: [
        "src/**/*.test.{ts,tsx}",
        "src/test/**",
        "src/main.tsx",
        "src/vite-env.d.ts",
        "src/types.ts",
      ],
      // Regression floor, set just below the current baseline. Ratchet these up
      // as component-test coverage (TEST-1) lands.
      thresholds: {
        statements: 76,
        branches: 67,
        functions: 71,
        lines: 78,
      },
    },
  },
});
