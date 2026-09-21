// ESLint 配置（扁平配置格式，ESLint 9+）
//
// 目标：对前端 TS 源码做**语法级**检查，不引入任何框架或运行时依赖。
// 只启用「可能出错」的规则，不做风格强制（风格交给 tsc + 编辑器）。
//
// 用法：
//   cd cmd/streamguard-gui/frontend
//   npm run lint
//
// 注意：类型检查由 tsc 负责（npm run typecheck），ESLint 只做语法级规则。

import parser from "@typescript-eslint/parser";

export default [
  {
    files: ["src/**/*.ts"],
    languageOptions: {
      parser,
      ecmaVersion: 2022,
      sourceType: "module",
      globals: {
        // 浏览器全局
        window: "readonly",
        document: "readonly",
        console: "readonly",
        fetch: "readonly",
        setTimeout: "readonly",
        clearTimeout: "readonly",
        setInterval: "readonly",
        clearInterval: "readonly",
        localStorage: "readonly",
        navigator: "readonly",
        alert: "readonly",
        confirm: "readonly",
        // Wails 运行时注入的全局
        runtime: "readonly",
      },
    },
    rules: {
      // 可能出错的规则（error）
      "no-undef": "off", // TS 自身负责未定义变量检查
      "no-unused-vars": [
        "warn",
        {
          args: "none",
          // 允许以 _ 开头的占位参数（如 catch (_) { /* 忽略 */ }）
          argsIgnorePattern: "^_",
          caughtErrorsIgnorePattern: "^_",
          varsIgnorePattern: "^_",
        },
      ],
      "no-redeclare": "error",
      "no-dupe-keys": "error",
      "no-dupe-args": "error",
      "no-unreachable": "error",
      "no-cond-assign": "error",
      "no-constant-condition": "warn",
      "no-empty": ["warn", { allowEmptyCatch: true }],
      "no-fallthrough": "error",
      "no-self-assign": "error",
      "no-sparse-arrays": "warn",
      "use-isnan": "error",
      "valid-typeof": "error",
      "no-async-promise-executor": "error",
      "require-atomic-updates": "warn",
      "no-unsafe-negation": "error",
      "no-unsafe-optional-chaining": "error",
    },
  },
  {
    ignores: ["dist/**", "wailsjs/**", "node_modules/**"],
  },
];
