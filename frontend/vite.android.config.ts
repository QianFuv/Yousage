/** Android app pages: the snapshot viewer embedded into the Android build of the Go app. */
import { defineConfig } from "vitest/config";
import { renameSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import desktopConfig from "./vite.config";

const OUT_DIR = fileURLToPath(new URL("./dist-android", import.meta.url));

export default defineConfig({
  ...desktopConfig,
  plugins: [
    ...(desktopConfig.plugins ?? []),
    {
      name: "android-index",
      /** The Wails asset server only serves a bundle that has an `index.html`. */
      writeBundle() {
        renameSync(join(OUT_DIR, "android.html"), join(OUT_DIR, "index.html"));
      },
    },
  ],
  build: {
    outDir: OUT_DIR,
    emptyOutDir: true,
    rollupOptions: {
      input: {
        android: fileURLToPath(new URL("./android.html", import.meta.url)),
      },
    },
  },
});
