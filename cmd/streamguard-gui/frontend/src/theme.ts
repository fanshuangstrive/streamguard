// 三态主题切换：跟随系统 → 浅色 → 深色（循环）。
//
// - 持久化到 localStorage（key: sg_theme），不入 config.json
// - system 模式下通过 matchMedia 实时跟随系统深浅色
// - 通过 <html data-theme="dark|light"> 切换 CSS 变量组

const THEME_KEY = "sg_theme";
const THEMES = ["system", "light", "dark"] as const;
export type ThemeMode = (typeof THEMES)[number];

const THEME_LABEL: Record<ThemeMode, string> = {
    system: "跟随系统",
    light: "浅色",
    dark: "深色",
};

const THEME_ICON: Record<ThemeMode, string> = {
    system: "🌓",
    light: "☀",
    dark: "☾",
};

const media = window.matchMedia("(prefers-color-scheme: dark)");

function isThemeMode(v: string | null): v is ThemeMode {
    return (THEMES as readonly string[]).includes(v ?? "");
}

export function getTheme(): ThemeMode {
    const v = localStorage.getItem(THEME_KEY);
    return isThemeMode(v) ? v : "system";
}

export function applyTheme(mode: ThemeMode): void {
    const dark = mode === "dark" || (mode === "system" && media.matches);
    // system 模式也显式设置 data-theme，保证与系统实时同步
    document.documentElement.dataset.theme = dark ? "dark" : "light";
}

// 绑定主题切换按钮：点击循环三态，图标/标题随状态变化（状态不依赖颜色）。
export function initTheme(btn: HTMLButtonElement): void {
    const syncButton = (mode: ThemeMode) => {
        btn.title = "主题：" + THEME_LABEL[mode];
        btn.textContent = THEME_ICON[mode];
    };

    const mode = getTheme();
    applyTheme(mode);
    syncButton(mode);

    btn.addEventListener("click", () => {
        const next = THEMES[(THEMES.indexOf(getTheme()) + 1) % THEMES.length];
        localStorage.setItem(THEME_KEY, next);
        applyTheme(next);
        syncButton(next);
    });

    // 系统主题变化时实时跟随（仅 system 模式）。
    media.addEventListener("change", () => {
        if (getTheme() === "system") applyTheme("system");
    });
}
