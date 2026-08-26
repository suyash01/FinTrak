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
  },
});
