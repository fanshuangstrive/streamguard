// 「关于」弹窗：轻量原生模态（零依赖、不含仓库地址等敏感域名）。
//
// 展示应用名、简介、版本号与 License。版本号集中在此处，后续可对接构建注入。
// 打开：顶栏 ℹ 图标；关闭：✕ 按钮 / 点击遮罩空白 / Esc。

const APP_VERSION = "0.1.0";

export function initAbout(btnOpen: HTMLButtonElement): void {
    const overlay = document.getElementById("aboutOverlay");
    const btnClose = document.getElementById("aboutClose") as HTMLButtonElement | null;
    if (!overlay || !btnClose) return;

    const versionEl = document.getElementById("aboutVersion");
    if (versionEl) versionEl.textContent = "v" + APP_VERSION;

    const open = (): void => {
        overlay.classList.add("show");
        btnClose.focus();
    };
    const close = (): void => {
        overlay.classList.remove("show");
    };

    btnOpen.addEventListener("click", open);
    btnClose.addEventListener("click", close);
    // 点击遮罩空白处关闭（点中弹窗本体不关闭）。
    overlay.addEventListener("click", (e) => {
        if (e.target === overlay) close();
    });
    // Esc 关闭，仅在弹窗显示时响应，避免干扰主视图快捷键。
    document.addEventListener("keydown", (e) => {
        if (e.key === "Escape" && overlay.classList.contains("show")) close();
    });
}
