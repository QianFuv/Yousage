/** Lint task-owned frontend code; unmodified source/generated bindings retain their upstream rules. */
import js from "@eslint/js";
import typescript from "typescript-eslint";
export default typescript.config(
  js.configs.recommended,
  ...typescript.configs.recommended,
  {
    languageOptions: {
      globals: {
        window: "readonly",
        document: "readonly",
        location: "readonly",
        requestAnimationFrame: "readonly",
        URLSearchParams: "readonly",
        MutationObserver: "readonly",
        console: "readonly",
      },
    },
  },
);
