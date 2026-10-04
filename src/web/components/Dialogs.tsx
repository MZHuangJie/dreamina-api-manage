import { useState } from "react";

import type { Account } from "../types.ts";
import { Field, Modal, Spinner } from "./ui.tsx";

/** 两个平台的登录入口与凭据位置不同，提示要分开写。 */
const PROVIDER_META: Record<string, { label: string; site: string; url: string; hint: React.ReactNode }> = {
  dreamina: {
    label: "Dreamina（海外版）",
    site: "dreamina.capcut.com",
    url: "https://dreamina.capcut.com",
    hint: (
      <>
        在{" "}
        <a href="https://dreamina.capcut.com" target="_blank" rel="noreferrer">
          dreamina.capcut.com
        </a>{" "}
        登录后，F12 → Application → Cookies → 找到 <code>.capcut.com</code> 域下的 <code>sessionid</code>。
        建议连整段 Cookie 一起粘贴——管理器会自动从里面识别 <code>store-idc</code>，据此选对区域集群。
      </>
    )
  },
  jimeng: {
    label: "即梦（国内版，已停用）",
    site: "jimeng.jianying.com",
    url: "https://jimeng.jianying.com",
    hint: (
      <>
        在{" "}
        <a href="https://jimeng.jianying.com" target="_blank" rel="noreferrer">
          jimeng.jianying.com
        </a>{" "}
        登录后，F12 → Application → Cookies → 复制 <code>sessionid</code> 的值。
        也可以直接粘贴整段 Cookie 或 <code>sessionid=...</code>。
      </>
    )
  }
};

export function AddAccountDialog({
  onClose,
  onSubmit
}: {
  onClose: () => void;
  onSubmit: (payload: Record<string, unknown>) => Promise<void>;
}) {
  const [provider, setProvider] = useState<"jimeng" | "dreamina">("dreamina");
  const [name, setName] = useState("");
  const [credential, setCredential] = useState("");
  const [remark, setRemark] = useState("");
  const [proxyUrl, setProxyUrl] = useState("");
  const [tags, setTags] = useState("");
  const [busy, setBusy] = useState(false);

  const meta = PROVIDER_META[provider]!;

  async function submit() {
    if (!credential.trim()) return;
    setBusy(true);
    try {
      await onSubmit({
        provider,
        name: name.trim() || undefined,
        sessionId: credential.trim(),
        remark: remark.trim() || undefined,
        proxyUrl: proxyUrl.trim() || undefined,
        tags: tags
          .split(/[,，\s]+/)
          .map((t) => t.trim())
          .filter(Boolean)
      });
      onClose();
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      title="添加账号"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose} disabled={busy}>
            取消
          </button>
          <button className="btn btn-primary" onClick={submit} disabled={busy || !credential.trim()}>
            {busy ? <Spinner /> : null} 添加并检测
          </button>
        </>
      }
    >
      <div className="alert alert-info" style={{ marginBottom: 13 }}>
        在 <a href="https://dreamina.capcut.com" target="_blank" rel="noreferrer">dreamina.capcut.com</a> 登录后，
        F12 → Application → Cookies → 找到 <code>.capcut.com</code> 域下的 <code>sessionid</code>。
        建议连整段 Cookie 一起粘贴——管理器会自动识别 <code>store-idc</code>，据此选对区域集群。
      </div>
      <Field label="登录凭据">
        <textarea
          className="textarea"
          rows={3}
          placeholder="粘贴 sessionid 或整段 Cookie"
          value={credential}
          onChange={(event) => setCredential(event.target.value)}
          autoFocus
        />
      </Field>
      <Field label="账号名称" hint="留空则自动按 sessionid 生成">
        <input
          className="input"
          placeholder="例如：主力号 / 抖音小号A"
          value={name}
          onChange={(event) => setName(event.target.value)}
        />
      </Field>
      <Field label="独立代理" hint="留空表示直连。格式：http://user:pass@host:port 或 socks5://host:port">
        <input
          className="input"
          placeholder="http://127.0.0.1:7890"
          value={proxyUrl}
          onChange={(event) => setProxyUrl(event.target.value)}
        />
      </Field>
      <Field label="标签" hint="用空格或逗号分隔，便于筛选">
        <input
          className="input"
          placeholder="主力 高积分"
          value={tags}
          onChange={(event) => setTags(event.target.value)}
        />
      </Field>
      <Field label="备注">
        <input
          className="input"
          placeholder="可选"
          value={remark}
          onChange={(event) => setRemark(event.target.value)}
        />
      </Field>
      <div className="field-hint">添加后会立即发起一次探活，验证凭据是否有效并读取积分。</div>
    </Modal>
  );
}

export function BulkImportDialog({
  onClose,
  onSubmit
}: {
  onClose: () => void;
  onSubmit: (text: string) => Promise<{ created: number; failed: number; errors: { line: number; reason: string }[] }>;
}) {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<{ created: number; failed: number; errors: { line: number; reason: string }[] } | null>(null);

  async function submit() {
    if (!text.trim()) return;
    setBusy(true);
    try {
      setResult(await onSubmit(text));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      title="批量导入账号"
      wide
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose} disabled={busy}>
            {result ? "完成" : "取消"}
          </button>
          <button className="btn btn-primary" onClick={submit} disabled={busy || !text.trim()}>
            {busy ? <Spinner /> : null} 开始导入
          </button>
        </>
      }
    >
      <Field
        label="每行一个账号"
        hint={
          <>
            支持两种格式：只写凭据，或 <code>名称 + Tab/逗号 + 凭据</code>。导入后不会自动探活，
            可在列表顶部点「全量探活」统一检测。
          </>
        }
      >
        <textarea
          className="textarea"
          rows={10}
          placeholder={"主力号\tsessionid_abc123...\n备用号,sessionid_def456..."}
          value={text}
          onChange={(event) => setText(event.target.value)}
          autoFocus
        />
      </Field>

      {result ? (
        <div className={result.failed ? "alert alert-warn" : "alert alert-info"}>
          成功导入 <strong>{result.created}</strong> 个，失败 <strong>{result.failed}</strong> 个。
          {result.errors.length ? (
            <div style={{ marginTop: 7 }}>
              {result.errors.slice(0, 8).map((error) => (
                <div key={error.line} className="mono">
                  第 {error.line} 行：{error.reason}
                </div>
              ))}
              {result.errors.length > 8 ? <div className="mono">…还有 {result.errors.length - 8} 条</div> : null}
            </div>
          ) : null}
        </div>
      ) : null}
    </Modal>
  );
}

export function EditAccountDialog({
  account,
  onClose,
  onSubmit
}: {
  account: Account;
  onClose: () => void;
  onSubmit: (payload: Record<string, unknown>) => Promise<void>;
}) {
  const [name, setName] = useState(account.name);
  const [remark, setRemark] = useState(account.remark);
  const [tags, setTags] = useState(account.tags.join(" "));
  const [proxyUrl, setProxyUrl] = useState(account.proxyUrlMasked);
  const [credential, setCredential] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit() {
    setBusy(true);
    try {
      const payload: Record<string, unknown> = {
        name: name.trim(),
        remark: remark.trim(),
        tags: tags.split(/[,，\s]+/).map((t) => t.trim()).filter(Boolean),
        proxyUrl: proxyUrl.trim()
      };
      if (credential.trim()) payload.sessionId = credential.trim();
      await onSubmit(payload);
      onClose();
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      title={`编辑「${account.name}」`}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose} disabled={busy}>
            取消
          </button>
          <button className="btn btn-primary" onClick={submit} disabled={busy}>
            {busy ? <Spinner /> : null} 保存
          </button>
        </>
      }
    >
      <Field label="账号名称">
        <input className="input" value={name} onChange={(event) => setName(event.target.value)} />
      </Field>
      <Field
        label="更换登录凭据"
        hint="留空表示不修改。粘贴新的 sessionid 后，旧凭据会被覆盖，账号状态会自动重置为「未检测」。"
      >
        <textarea
          className="textarea"
          rows={2}
          placeholder="留空则不修改"
          value={credential}
          onChange={(event) => setCredential(event.target.value)}
        />
      </Field>
      <Field label="独立代理" hint="留空表示取消代理、恢复直连">
        <input className="input" value={proxyUrl} onChange={(event) => setProxyUrl(event.target.value)} />
      </Field>
      <Field label="标签">
        <input className="input" value={tags} onChange={(event) => setTags(event.target.value)} />
      </Field>
      <Field label="备注">
        <input className="input" value={remark} onChange={(event) => setRemark(event.target.value)} />
      </Field>
    </Modal>
  );
}

export function ConfirmDialog({
  title,
  message,
  confirmLabel = "确认",
  danger,
  onClose,
  onConfirm
}: {
  title: string;
  message: React.ReactNode;
  confirmLabel?: string;
  danger?: boolean;
  onClose: () => void;
  onConfirm: () => Promise<void> | void;
}) {
  const [busy, setBusy] = useState(false);
  return (
    <Modal
      title={title}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose} disabled={busy}>
            取消
          </button>
          <button
            className={danger ? "btn btn-danger" : "btn btn-primary"}
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              try {
                await onConfirm();
                onClose();
              } finally {
                setBusy(false);
              }
            }}
          >
            {busy ? <Spinner /> : null} {confirmLabel}
          </button>
        </>
      }
    >
      <div style={{ fontSize: 13.5 }}>{message}</div>
    </Modal>
  );
}
