// 轻量非阻塞 Toast 通知（可带动作按钮，如「去修改端口」）。
//
// 用法：showToast("配置已保存", "success")
//       showToast("启动失败：...",
//               "error", { label: "去修改端口", onClick: () => focusField() })
// 类型：info（默认，accent 边）/ success（绿边）/ error（红边）

export type ToastType = "info" | "success" | "error";

export interface ToastAction {
    label: string;
    onClick: () => void;
}

export function showToast(msg: string, type: ToastType = "info", action?: ToastAction): void {
    const wrap = document.getElementById("toastWrap");
    if (!wrap) return;
    const t = document.createElement("div");
    t.className = `toast ${type}`;
    const span = document.createElement("span");
    span.textContent = msg;
    t.appendChild(span);
    if (action) {
        const btn = document.createElement("button");
        btn.className = "toast-action";
        btn.textContent = action.label;
        btn.addEventListener("click", () => {
            t.remove();
            action.onClick();
        });
        t.appendChild(btn);
    }
    wrap.appendChild(t);
    setTimeout(() => {
        t.classList.add("hide");
        setTimeout(() => t.remove(), 300);
    }, action ? 6000 : 2600);
}
