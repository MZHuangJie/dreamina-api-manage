import { useCallback, useEffect, useState } from "react";

import { api } from "../api.ts";
import type { APIKey } from "../types.ts";
import { Field, Modal, Spinner } from "./ui.tsx";

/** 把一段文本复制到剪贴板，并给出反馈。 */
function useCopy(onToast: (text: string, tone?: "success" | "error" | "info") => void) {
  return useCallback(
    async (text: string, label: string) => {
      try {
        await navigator.clipboard.writeText(text);
        onToast(`${label}已复制`, "success");
      } catch {
        onToast("复制失败，请手动选中", "error");
      }
    },
    [onToast]
  );
}

function CodeBlock({
  code,
  onCopy,
  label
}: {
  code: string;
  onCopy: (text: string, label: string) => void;
  label: string;
}) {
  return (
    <div style={{ position: "relative" }}>
      <pre
        className="mono"
        style={{
          background: "var(--bg-sunken, #14161c)",
          border: "1px solid var(--border)",
          borderRadius: 8,
          padding: "11px 13px",
          fontSize: 12,
          lineHeight: 1.65,
          overflowX: "auto",
          margin: 0,
          whiteSpace: "pre"
        }}
      >
        {code}
      </pre>
      <button
        className="btn"
        style={{ position: "absolute", top: 8, right: 8, fontSize: 11, padding: "3px 8px" }}
        onClick={() => void onCopy(code, label)}
      >
        复制
      </button>
    </div>
  );
}

export function GatewayPanel({
  onToast
}: {
  onToast: (text: string, tone?: "success" | "error" | "info") => void;
}) {
  const [keys, setKeys] = useState<APIKey[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [creating, setCreating] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [freshSecret, setFreshSecret] = useState<{ name: string; secret: string } | null>(null);
  const [pendingDelete, setPendingDelete] = useState<APIKey | null>(null);

  const copy = useCopy(onToast);
  const baseUrl = `${location.origin}/v1`;

  const refresh = useCallback(async () => {
    try {
      setKeys(await api.listKeys());
    } catch (error) {
      onToast(`加载密钥失败：${(error as Error).message}`, "error");
    } finally {
      setLoaded(true);
    }
  }, [onToast]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  async function toggle(key: APIKey) {
    setBusyId(key.id);
    try {
      await api.setKeyEnabled(key.id, !key.enabled);
      onToast(key.enabled ? `「${key.name}」已吊销` : `「${key.name}」已启用`, "info");
      await refresh();
    } catch (error) {
      onToast((error as Error).message, "error");
    } finally {
      setBusyId(null);
    }
  }

  async function remove(key: APIKey) {
    setBusyId(key.id);
    try {
      await api.deleteKey(key.id);
      onToast(`已删除「${key.name}」`, "info");
      setPendingDelete(null);
      await refresh();
    } catch (error) {
      onToast((error as Error).message, "error");
    } finally {
      setBusyId(null);
    }
  }

  const curlSample = `curl ${baseUrl}/images/generations \\
  -H "Authorization: Bearer sk-dm-你的密钥" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "high_aes_general_v50",
    "prompt": "a ripe strawberry on white marble",
    "size": "1024x1024"
  }'`;

  const pythonSample = `from openai import OpenAI

client = OpenAI(
    base_url="${baseUrl}",
    api_key="sk-dm-你的密钥",
)

result = client.images.generate(
    model="high_aes_general_v50",
    prompt="a ripe strawberry on white marble",
    size="1024x1024",
)

print(result.data[0].url)`;

  return (
    <>
      <div className="panel">
        <div className="panel-head">
          <div>
            <div className="panel-title">聚合网关</div>
            <div className="panel-sub">
              外部程序用下面的地址和密钥即可调用账号池出图，协议与 OpenAI 图片接口兼容
            </div>
          </div>
        </div>

        <div className="kv-grid" style={{ marginBottom: 14 }}>
          <div className="kv">
            <div className="kv-label">Base URL</div>
            <div className="mono" style={{ fontSize: 13, display: "flex", alignItems: "center", gap: 8 }}>
              {baseUrl}
              <button
                className="btn"
                style={{ fontSize: 11, padding: "2px 7px" }}
                onClick={() => void copy(baseUrl, "Base URL ")}
              >
                复制
              </button>
            </div>
          </div>
          <div className="kv">
            <div className="kv-label">鉴权</div>
            <div className="mono" style={{ fontSize: 13 }}>
              Authorization: Bearer sk-dm-…
            </div>
          </div>
          <div className="kv">
            <div className="kv-label">同步 / 异步</div>
            <div style={{ fontSize: 13 }}>
              默认同步等出图；加 <code>?async=1</code> 立即返回任务 id
            </div>
          </div>
        </div>

        <div className="alert alert-info">
          网关调用会走账号池的调度策略（当前用「失败最少优先」自动挑账号），并且
          <strong>同样记进生成历史</strong>——在「生成」页能看到是哪把密钥发起的。
        </div>
      </div>

      <div className="panel">
        <div
          className="panel-head"
          style={{ display: "flex", alignItems: "flex-start", justifyContent: "space-between", gap: 12 }}
        >
          <div>
            <div className="panel-title">接入密钥</div>
            <div className="panel-sub">
              明文只在创建时显示一次，请立即保存。库里只存哈希，找不回。
            </div>
          </div>
          <button
            className="btn btn-primary"
            style={{ flexShrink: 0 }}
            onClick={() => setCreating(true)}
          >
            + 新建密钥
          </button>
        </div>

        {!loaded ? (
          <Spinner />
        ) : keys.length === 0 ? (
          <div className="empty">
            还没有密钥。新建一把，就能让外部程序接入了。
          </div>
        ) : (
          <table className="table">
            <thead>
              <tr>
                <th>名称</th>
                <th>密钥</th>
                <th>调用次数</th>
                <th>今日 / 配额</th>
                <th>限制</th>
                <th>最近使用</th>
                <th>状态</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {keys.map((key) => (
                <tr key={key.id}>
                  <td>
                    <div style={{ fontWeight: 500 }}>{key.name}</div>
                    {key.remark ? (
                      <div style={{ fontSize: 11, color: "var(--text-dim)" }}>{key.remark}</div>
                    ) : null}
                  </td>
                  <td className="mono" style={{ fontSize: 12 }}>
                    {key.keyPrefix}
                  </td>
                  <td>{key.usageCount}</td>
                  <td style={{ fontSize: 12 }}>
                    {key.todayUsed}
                    {key.dailyQuota > 0 ? ` / ${key.dailyQuota}` : " / 不限"}
                  </td>
                  <td style={{ fontSize: 11, color: "var(--text-dim)", lineHeight: 1.6 }}>
                    {key.ratePerMinute > 0 ? `${key.ratePerMinute}/分钟` : "不限频次"}
                    <br />
                    {key.maxConcurrent > 0 ? `并发 ${key.maxConcurrent}` : "并发默认"}
                  </td>
                  <td style={{ fontSize: 12, color: "var(--text-dim)" }}>
                    {key.lastUsedAt ? key.lastUsedAt.replace("T", " ").slice(0, 16) : "—"}
                  </td>
                  <td>
                    {key.enabled ? (
                      <span className="badge badge-accent">启用</span>
                    ) : (
                      <span className="badge badge-gray">已吊销</span>
                    )}
                  </td>
                  <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                    <button
                      className="btn"
                      disabled={busyId === key.id}
                      onClick={() => void toggle(key)}
                    >
                      {key.enabled ? "吊销" : "启用"}
                    </button>{" "}
                    <button
                      className="btn btn-danger"
                      disabled={busyId === key.id}
                      onClick={() => setPendingDelete(key)}
                    >
                      删除
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="panel">
        <div className="panel-head">
          <div>
            <div className="panel-title">接入示例</div>
            <div className="panel-sub">把密钥替换进去即可运行</div>
          </div>
        </div>

        <div className="kv-label" style={{ marginBottom: 7 }}>curl</div>
        <CodeBlock code={curlSample} onCopy={copy} label="curl 示例 " />

        <div className="kv-label" style={{ margin: "16px 0 7px" }}>OpenAI Python SDK</div>
        <CodeBlock code={pythonSample} onCopy={copy} label="Python 示例 " />

        <div className="kv-label" style={{ margin: "16px 0 7px" }}>参数说明</div>
        <table className="table">
          <thead>
            <tr>
              <th>参数</th>
              <th>说明</th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <td className="mono">prompt</td>
              <td>必填，提示词</td>
            </tr>
            <tr>
              <td className="mono">model</td>
              <td>
                可选。默认 <code>high_aes_general_v50</code>（Seedream 5.0）。
                也接受 <code>dall-e-3</code> / <code>gpt-image-1</code> 这类别名，会自动映射
              </td>
            </tr>
            <tr>
              <td className="mono">size</td>
              <td>
                可选，如 <code>1024x1024</code>、<code>1792x1024</code>。
                <strong>只用来决定宽高比</strong>——平台原生出图分辨率是 2048 长边，
                强行要 1024 没有意义
              </td>
            </tr>
            <tr>
              <td className="mono">ratio</td>
              <td>
                扩展参数，直接指定比例：<code>1:1</code> <code>16:9</code> <code>9:16</code>{" "}
                <code>4:3</code> <code>3:4</code> <code>3:2</code> <code>2:3</code> <code>21:9</code>
              </td>
            </tr>
            <tr>
              <td className="mono">account_id</td>
              <td>扩展参数，强制用某个账号出图（默认自动挑）</td>
            </tr>
          </tbody>
        </table>
      </div>

      {creating ? (
        <CreateKeyDialog
          onClose={() => setCreating(false)}
          onCreated={(name, secret) => {
            setCreating(false);
            setFreshSecret({ name, secret });
            void refresh();
          }}
        />
      ) : null}

      {freshSecret ? (
        <Modal
          title="密钥已创建"
          onClose={() => setFreshSecret(null)}
          footer={
            <button className="btn btn-primary" onClick={() => setFreshSecret(null)}>
              我已保存
            </button>
          }
        >
          <div className="alert alert-warn" style={{ marginBottom: 13 }}>
            这是「{freshSecret.name}」的明文密钥，<strong>只显示这一次</strong>。
            关掉就再也看不到了，只能重新建一把。
          </div>
          <Field label="API Key">
            <div style={{ display: "flex", gap: 8 }}>
              <input className="input mono" readOnly value={freshSecret.secret} />
              <button className="btn" onClick={() => void copy(freshSecret.secret, "密钥 ")}>
                复制
              </button>
            </div>
          </Field>
        </Modal>
      ) : null}

      {pendingDelete ? (
        <Modal
          title="删除密钥"
          onClose={() => setPendingDelete(null)}
          footer={
            <>
              <button className="btn" onClick={() => setPendingDelete(null)}>
                取消
              </button>
              <button
                className="btn btn-danger"
                disabled={busyId === pendingDelete.id}
                onClick={() => void remove(pendingDelete)}
              >
                {busyId === pendingDelete.id ? <Spinner /> : null} 确认删除
              </button>
            </>
          }
        >
          确定删除「{pendingDelete.name}」？使用这把密钥的调用方会立即失效。
          如果只是想临时停用，用「吊销」更合适。
        </Modal>
      ) : null}
    </>
  );
}

function CreateKeyDialog({
  onClose,
  onCreated
}: {
  onClose: () => void;
  onCreated: (name: string, secret: string) => void;
}) {
  const [name, setName] = useState("");
  const [remark, setRemark] = useState("");
  const [ratePerMinute, setRatePerMinute] = useState("10");
  const [maxConcurrent, setMaxConcurrent] = useState("2");
  const [dailyQuota, setDailyQuota] = useState("0");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function toNumber(raw: string): number {
    const n = Number.parseInt(raw, 10);
    return Number.isFinite(n) && n > 0 ? n : 0;
  }

  async function submit() {
    if (!name.trim()) return;
    setBusy(true);
    setError(null);
    try {
      const result = await api.createKey({
        name: name.trim(),
        remark: remark.trim() || undefined,
        ratePerMinute: toNumber(ratePerMinute),
        maxConcurrent: toNumber(maxConcurrent),
        dailyQuota: toNumber(dailyQuota)
      });
      onCreated(result.key.name, result.secret);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      title="新建接入密钥"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose} disabled={busy}>
            取消
          </button>
          <button className="btn btn-primary" onClick={submit} disabled={busy || !name.trim()}>
            {busy ? <Spinner /> : null} 创建
          </button>
        </>
      }
    >
      {error ? <div className="alert alert-error" style={{ marginBottom: 12 }}>{error}</div> : null}
      <Field label="名称" hint="用来辨认是谁在用，比如「我的脚本」">
        <input
          className="input"
          placeholder="例如：n8n 工作流 / 测试脚本"
          value={name}
          onChange={(event) => setName(event.target.value)}
          autoFocus
        />
      </Field>
      <Field label="备注" hint="可选">
        <input
          className="input"
          placeholder="例如：给运营同事用，只跑商品图"
          value={remark}
          onChange={(event) => setRemark(event.target.value)}
        />
      </Field>

      <div className="kv-label" style={{ margin: "4px 0 8px" }}>
        用量限制（留空或填 0 表示不限）
      </div>
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr 1fr", gap: 10 }}>
        <Field label="每分钟调用">
          <input
            className="input"
            type="number"
            min={0}
            value={ratePerMinute}
            onChange={(event) => setRatePerMinute(event.target.value)}
          />
        </Field>
        <Field label="同时进行">
          <input
            className="input"
            type="number"
            min={0}
            value={maxConcurrent}
            onChange={(event) => setMaxConcurrent(event.target.value)}
          />
        </Field>
        <Field label="每日配额">
          <input
            className="input"
            type="number"
            min={0}
            value={dailyQuota}
            onChange={(event) => setDailyQuota(event.target.value)}
          />
        </Field>
      </div>
      <div className="field-hint" style={{ marginTop: -4 }}>
        「同时进行」建议不超过服务器总并发；「每日配额」按积分估，比如 200 次约等于 400 张图。
      </div>
    </Modal>
  );
}
