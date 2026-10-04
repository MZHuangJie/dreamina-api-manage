import type { ReactNode } from "react";

import type { HealthStatus } from "../types.ts";

export function relTime(iso: string | null | undefined): string {
  if (!iso) return "—";
  const diff = Date.now() - new Date(iso).getTime();
  if (diff < 0) return "刚刚";
  const seconds = Math.floor(diff / 1000);
  if (seconds < 60) return `${seconds} 秒前`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days} 天前`;
  return new Date(iso).toLocaleDateString("zh-CN");
}

export const HEALTH_META: Record<HealthStatus, { label: string; tone: string; hint: string }> = {
  healthy: { label: "正常", tone: "badge-green", hint: "登录态有效" },
  expired: { label: "登录失效", tone: "badge-red", hint: "sessionid 已过期，需要重新登录" },
  error: { label: "异常", tone: "badge-amber", hint: "网络或代理问题，可重试" },
  unknown: { label: "未检测", tone: "badge-gray", hint: "尚未探活" }
};

export function HealthBadge({ health }: { health: HealthStatus }) {
  const meta = HEALTH_META[health];
  return (
    <span className={`badge ${meta.tone}`} title={meta.hint}>
      <span className="dot" />
      {meta.label}
    </span>
  );
}

export function Badge({ tone = "gray", children }: { tone?: string; children: ReactNode }) {
  return <span className={`badge badge-${tone}`}>{children}</span>;
}

export function Empty({ title, hint, icon = "◎" }: { title: string; hint?: string; icon?: string }) {
  return (
    <div className="empty">
      <div className="empty-icon">{icon}</div>
      <div className="empty-title">{title}</div>
      {hint ? <div>{hint}</div> : null}
    </div>
  );
}

export function Modal({
  title,
  onClose,
  children,
  footer,
  wide
}: {
  title: string;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
  wide?: boolean;
}) {
  return (
    <div className="overlay" onMouseDown={(event) => event.target === event.currentTarget && onClose()}>
      <div className={`modal${wide ? " wide" : ""}`}>
        <div className="modal-head">
          <div className="modal-title">{title}</div>
          <button className="btn btn-ghost btn-sm" onClick={onClose} aria-label="关闭">
            ✕
          </button>
        </div>
        <div className="modal-body">{children}</div>
        {footer ? <div className="modal-foot">{footer}</div> : null}
      </div>
    </div>
  );
}

export function Field({
  label,
  hint,
  children
}: {
  label: string;
  hint?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="field">
      <label className="field-label">{label}</label>
      {children}
      {hint ? <div className="field-hint">{hint}</div> : null}
    </div>
  );
}

export function Spinner() {
  return <span className="spin">◌</span>;
}
