import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { api } from "../api.ts";
import type { Account, GenerationMode, GenerationRecord, ModelCatalog } from "../types.ts";
import { Field, Spinner } from "./ui.tsx";

const MODES: { id: GenerationMode; label: string; kind: "image" | "video"; hint: string }[] = [
  { id: "text2image", label: "文生图", kind: "image", hint: "用提示词直接生成图片" },
  { id: "image2image", label: "图生图", kind: "image", hint: "上传参考图，按提示词改造（最多 10 张）" },
  { id: "text2video", label: "文生视频", kind: "video", hint: "用提示词生成视频" },
  { id: "image2video", label: "图生视频", kind: "video", hint: "上传首帧图，让它动起来" }
];

function readAsDataUrl(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error ?? new Error("读取文件失败"));
    reader.readAsDataURL(file);
  });
}

const STATUS_META: Record<string, { label: string; tone: string }> = {
  pending: { label: "排队中", tone: "badge-gray" },
  running: { label: "生成中", tone: "badge-accent" },
  succeeded: { label: "已完成", tone: "badge-green" },
  failed: { label: "失败", tone: "badge-red" },
  canceled: { label: "已取消", tone: "badge-gray" }
};

export function GeneratePanel({
  accounts,
  onToast
}: {
  accounts: Account[];
  onToast: (text: string, tone?: "success" | "error" | "info") => void;
}) {
  const [catalog, setCatalog] = useState<ModelCatalog | null>(null);
  const [mode, setMode] = useState<GenerationMode>("text2image");
  const [prompt, setPrompt] = useState("");
  const [negativePrompt, setNegativePrompt] = useState("");
  const [modelId, setModelId] = useState("");
  const [ratio, setRatio] = useState("");
  const [resolution, setResolution] = useState("");
  const [count, setCount] = useState(1);
  const [duration, setDuration] = useState(5);
  const [accountId, setAccountId] = useState("");
  const [references, setReferences] = useState<string[]>([]);
  const [firstFrame, setFirstFrame] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [records, setRecords] = useState<GenerationRecord[]>([]);
  const fileInput = useRef<HTMLInputElement>(null);

  const modeMeta = MODES.find((m) => m.id === mode)!;
  const isVideo = modeMeta.kind === "video";

  const models = useMemo(() => {
    if (!catalog) return [];
    return isVideo ? catalog.video : catalog.image;
  }, [catalog, isVideo]);

  const ratios = useMemo(() => {
    if (!catalog) return [];
    return isVideo ? catalog.videoRatios : catalog.imageRatios;
  }, [catalog, isVideo]);

  const currentModel = useMemo(
    () => models.find((m) => m.id === modelId) ?? models[0] ?? null,
    [models, modelId]
  );

  const creditEstimate = useMemo(() => {
    if (!currentModel) return 0;
    if (isVideo) {
      const perSecond = (currentModel as { creditPerSecond: number }).creditPerSecond ?? 0;
      return perSecond * duration;
    }
    return ((currentModel as { creditPerImage: number }).creditPerImage ?? 0) * count;
  }, [currentModel, isVideo, count, duration]);

  /* ------------------------------ 初始化 ------------------------------ */

  useEffect(() => {
    api.models().then(setCatalog).catch(() => setCatalog(null));
  }, []);

  // 切换图片/视频时重置模型与比例
  useEffect(() => {
    if (!catalog) return;
    const pool = isVideo ? catalog.video : catalog.image;
    const stillValid = pool.some((m) => m.id === modelId);
    if (!stillValid) {
      const preferred = isVideo ? "jimeng-video-seedance-2.0" : "jimeng-image-5.0-lite";
      setModelId(pool.find((m) => m.id === preferred)?.id ?? pool[0]?.id ?? "");
    }
    setRatio("");
    setResolution("");
    setReferences([]);
    setFirstFrame(null);
  }, [isVideo, catalog, modelId]);

  /* ------------------------------ 记录轮询 ------------------------------ */

  const refreshRecords = useCallback(async () => {
    try {
      setRecords(await api.generations(40));
    } catch {
      /* 轮询失败静默重试 */
    }
  }, []);

  useEffect(() => {
    void refreshRecords();
  }, [refreshRecords]);

  const active = records.filter((r) => r.status === "pending" || r.status === "running");
  useEffect(() => {
    if (!active.length) return;
    const timer = setInterval(() => void refreshRecords(), 2000);
    return () => clearInterval(timer);
  }, [active.length, refreshRecords]);

  /* ------------------------------ 提交 ------------------------------ */

  async function submit() {
    if (mode !== "image2image" && !prompt.trim()) {
      onToast("请先填写提示词", "error");
      return;
    }
    if (mode === "image2image" && references.length === 0) {
      onToast("图生图至少需要一张参考图", "error");
      return;
    }
    if (mode === "image2video" && !firstFrame) {
      onToast("图生视频需要上传首帧图", "error");
      return;
    }

    setSubmitting(true);
    try {
      await api.createGeneration({
        kind: modeMeta.kind,
        mode,
        prompt: prompt.trim(),
        accountId: accountId || undefined,
        modelId: modelId || undefined,
        ratio: ratio || undefined,
        resolution: resolution || undefined,
        count: isVideo ? undefined : count,
        duration: isVideo ? duration : undefined,
        negativePrompt: negativePrompt.trim() || undefined,
        referenceImages: mode === "image2image" ? references : undefined,
        firstFrame: mode === "image2video" ? (firstFrame ?? undefined) : undefined
      });
      onToast("已提交生成任务", "success");
      await refreshRecords();
    } catch (error) {
      onToast((error as Error).message, "error");
    } finally {
      setSubmitting(false);
    }
  }

  async function pickFiles(files: FileList | null, target: "reference" | "firstFrame") {
    if (!files?.length) return;
    try {
      if (target === "firstFrame") {
        setFirstFrame(await readAsDataUrl(files[0]!));
      } else {
        const urls = await Promise.all(Array.from(files).slice(0, 10).map(readAsDataUrl));
        setReferences((current) => [...current, ...urls].slice(0, 10));
      }
    } catch (error) {
      onToast((error as Error).message, "error");
    }
  }

  /* ------------------------------ 渲染 ------------------------------ */

  return (
    <>
      <div className="panel">
        <div className="tabs">
          {MODES.map((item) => (
            <button
              key={item.id}
              className={`tab${mode === item.id ? " active" : ""}`}
              onClick={() => setMode(item.id)}
              title={item.hint}
            >
              {item.label}
            </button>
          ))}
        </div>

        <Field label="提示词" hint={modeMeta.hint}>
          <textarea
            className="textarea"
            style={{ fontFamily: "inherit", fontSize: 13.5, minHeight: 92 }}
            rows={4}
            placeholder={
              isVideo
                ? "例如：女孩在沙滩奔跑，夕阳逆光，电影感镜头"
                : "例如：一只在太空飞行的柴犬，赛博朋克风格，高细节"
            }
            value={prompt}
            onChange={(event) => setPrompt(event.target.value)}
          />
        </Field>

        {mode === "image2image" ? (
          <Field label={`参考图（${references.length}/10）`} hint="支持多选。生成时会按参考图改造画面。">
            <div className="ref-grid">
              {references.map((url, index) => (
                <div className="ref-thumb" key={index}>
                  <img src={url} alt={`参考图 ${index + 1}`} />
                  <button
                    className="ref-remove"
                    onClick={() => setReferences((c) => c.filter((_, i) => i !== index))}
                    title="移除"
                  >
                    ✕
                  </button>
                </div>
              ))}
              <button className="ref-add" onClick={() => fileInput.current?.click()}>
                ＋<span>添加</span>
              </button>
            </div>
          </Field>
        ) : null}

        {mode === "image2video" ? (
          <Field label="首帧图" hint="视频会从这张图开始运动">
            <div className="ref-grid">
              {firstFrame ? (
                <div className="ref-thumb">
                  <img src={firstFrame} alt="首帧" />
                  <button className="ref-remove" onClick={() => setFirstFrame(null)} title="移除">
                    ✕
                  </button>
                </div>
              ) : (
                <button className="ref-add" onClick={() => fileInput.current?.click()}>
                  ＋<span>上传</span>
                </button>
              )}
            </div>
          </Field>
        ) : null}

        <input
          ref={fileInput}
          type="file"
          accept="image/*"
          multiple={mode === "image2image"}
          style={{ display: "none" }}
          onChange={(event) => {
            void pickFiles(event.target.files, mode === "image2video" ? "firstFrame" : "reference");
            event.target.value = "";
          }}
        />

        <div className="form-grid">
          <Field label="模型">
            <select className="select" value={modelId} onChange={(e) => setModelId(e.target.value)}>
              {models.map((model) => (
                <option key={model.id} value={model.id}>
                  {model.label}
                </option>
              ))}
            </select>
          </Field>

          <Field label="画面比例">
            <select className="select" value={ratio} onChange={(e) => setRatio(e.target.value)}>
              <option value="">自动（按提示词判断）</option>
              {ratios.map((item) => (
                <option key={item} value={item}>
                  {item}
                </option>
              ))}
            </select>
          </Field>

          <Field label="分辨率">
            <select className="select" value={resolution} onChange={(e) => setResolution(e.target.value)}>
              <option value="">默认（{currentModel?.defaultResolution ?? "—"}）</option>
              {(currentModel?.resolutions ?? []).map((item) => (
                <option key={item} value={item}>
                  {item}
                </option>
              ))}
            </select>
          </Field>

          {isVideo ? (
            <Field label="时长">
              <select
                className="select"
                value={duration}
                onChange={(e) => setDuration(Number(e.target.value))}
              >
                {((currentModel as { durations?: number[] })?.durations ?? [5]).map((item) => (
                  <option key={item} value={item}>
                    {item} 秒
                  </option>
                ))}
              </select>
            </Field>
          ) : (
            <Field label="生成数量">
              <select className="select" value={count} onChange={(e) => setCount(Number(e.target.value))}>
                {[1, 2, 3, 4].map((item) => (
                  <option key={item} value={item}>
                    {item} 张
                  </option>
                ))}
              </select>
            </Field>
          )}

          <Field label="使用账号" hint="自动 = 按当前调度策略挑选">
            <select className="select" value={accountId} onChange={(e) => setAccountId(e.target.value)}>
              <option value="">自动选择</option>
              {accounts.map((account) => (
                <option key={account.id} value={account.id}>
                  {account.name}
                  {account.totalCredit !== null ? `（${account.totalCredit} 积分）` : ""}
                </option>
              ))}
            </select>
          </Field>

          {!isVideo ? (
            <Field label="负向提示词" hint="部分模型支持，可留空">
              <input
                className="input"
                value={negativePrompt}
                onChange={(e) => setNegativePrompt(e.target.value)}
                placeholder="不希望出现的元素"
              />
            </Field>
          ) : null}
        </div>

        <div className="row-between" style={{ marginTop: 4 }}>
          <div className="field-hint">
            预计消耗 <strong style={{ color: "var(--accent)" }}>{creditEstimate}</strong> 积分
          </div>
          <button className="btn btn-primary" onClick={submit} disabled={submitting}>
            {submitting ? <Spinner /> : null} 开始生成
          </button>
        </div>
      </div>

      {active.length ? (
        <div className="panel">
          <div className="panel-title">进行中（{active.length}）</div>
          <div className="task-list">
            {active.map((record) => (
              <div className="task" key={record.id}>
                <div className="task-head">
                  <span className={`badge ${STATUS_META[record.status]?.tone ?? "badge-gray"}`}>
                    {record.status === "running" ? <Spinner /> : null}
                    {STATUS_META[record.status]?.label ?? record.status}
                  </span>
                  <span className="chip">{record.modelLabel}</span>
                  <span className="mono grow" style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                    {record.prompt || "（无提示词）"}
                  </span>
                  <button
                    className="btn btn-sm"
                    onClick={async () => {
                      await api.cancelGeneration(record.id).catch(() => {});
                      await refreshRecords();
                    }}
                  >
                    取消
                  </button>
                </div>
                <div className="field-hint" style={{ marginTop: 4 }}>
                  {record.progress ?? "已提交…"} · 账号 {record.accountName ?? "—"}
                </div>
                <div className="progress">
                  <div className="progress-bar indeterminate" />
                </div>
              </div>
            ))}
          </div>
        </div>
      ) : null}

      <div className="panel">
        <div className="panel-title">生成记录</div>
        {records.length === 0 ? (
          <div style={{ color: "var(--muted)", fontSize: 12.5, padding: "8px 0" }}>
            还没有生成记录。填写提示词后点击「开始生成」。
          </div>
        ) : (
          <div className="result-list">
            {records.map((record) => (
              <div className="result" key={record.id}>
                <div className="result-head">
                  <span className={`badge ${STATUS_META[record.status]?.tone ?? "badge-gray"}`}>
                    {STATUS_META[record.status]?.label ?? record.status}
                  </span>
                  <span className="chip">{record.mode}</span>
                  <span className="chip">{record.modelLabel}</span>
                  <span className="spacer mono">{new Date(record.createdAt).toLocaleString("zh-CN")}</span>
                  <button
                    className="btn btn-ghost btn-sm"
                    title="删除记录"
                    onClick={async () => {
                      await api.deleteGeneration(record.id).catch(() => {});
                      await refreshRecords();
                    }}
                  >
                    ✕
                  </button>
                </div>
                <div className="result-prompt">{record.prompt || "（无提示词）"}</div>

                {record.error ? <div className="alert alert-error" style={{ marginTop: 8, marginBottom: 0 }}>{record.error}</div> : null}

                {record.status === "succeeded" && record.result ? (
                  <div className="result-media">
                    {(record.result.urls ?? []).map((url, index) => (
                      <a key={index} href={url} target="_blank" rel="noreferrer" className="media-thumb">
                        <img src={url} alt={`结果 ${index + 1}`} loading="lazy" />
                      </a>
                    ))}
                    {record.result.url ? (
                      <video src={record.result.url} controls preload="metadata" className="media-video" />
                    ) : null}
                  </div>
                ) : null}
              </div>
            ))}
          </div>
        )}
      </div>
    </>
  );
}
