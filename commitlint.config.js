export default {
  extends: ["@commitlint/config-conventional"],
  rules: {
    "scope-enum": [
      1,
      "always",
      ["engine", "cli", "review", "agents", "trust", "sandbox", "web", "ide", "ci", "docs", "deps"],
    ],
  },
};
