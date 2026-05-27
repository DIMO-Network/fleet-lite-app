import eslint from "@eslint/js";
import tseslint from "typescript-eslint";
import lit from "eslint-plugin-lit";
import globals from "globals";

export default tseslint.config(
  eslint.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ["**/*.ts", "**/*.js"],
    languageOptions: {
      globals: {
        ...globals.browser,
        ...globals.es2022,
      },
    },
    plugins: {
      lit,
    },
    rules: {
      ...lit.configs.recommended.rules,
      "no-console": "warn",
      "@typescript-eslint/no-explicit-any": "warn",
      "@typescript-eslint/no-unused-vars": ["warn", { argsIgnorePattern: "^_" }],
      "semi": ["error", "always"],
    },
  },
  {
    ignores: ["dist/**", "node_modules/**"],
  }
);
