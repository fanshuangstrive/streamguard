// ESLint 配置（扁平配置格式，ESLint 9+）
//
// 目标：对原生 JS 前端做**语法级**检查，不引入任何框架或运行时依赖。
// 只启用「可能出错」的规则，不做风格强制（风格交给编辑器/格式化工具）。
//
// 用法：
//   cd cmd/streamguard-gui/frontend
//   npx eslint src/
//
// 注意：本配置不加入 package.json 的 devDependencies（保持零依赖），
// 需要时用 npx 临时拉取 eslint 即可。

export default [
  {
    files: ["src/**/*.js"],
    languageOptions: {
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
      "no-undef": "error",
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
