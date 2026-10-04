import { db, logEvent, nowIso, nz } from "../db.ts";
import { newId } from "../crypto.ts";
import { AccountError, markFailure, markSuccess, selectAccount } from "../accounts/pool.ts";
import type { AccountCredential, SelectionStrategy } from "../accounts/pool.ts";
import { estimateCredit, resolveImageModel, resolveVideoModel } from "../jimeng/models.ts";
import { generateImage, generateVideo } from "../jimeng/generate.ts";
import { JimengError } from "../jimeng/errors.ts";
import type { ImageToImageOptions, TextToImageOptions, VideoOptions } from "../jimeng/generate.ts";

export type GenerationKind = "image" | "video";
export type GenerationMode = "text2image" | "image2image" | "text2video" | "image2video";
export type GenerationStatus = "pending" | "running" | "succeeded" | "failed" | "canceled";

export interface GenerationRecord {
  id: string;
  accountId: string | null;
  accountName: string | null;
  kind: GenerationKind;
  mode: GenerationMode;
  model: string;
  modelLabel: string;
  prompt: string;
  params: Record<string, unknown>;
  status: GenerationStatus;
  progress: string | null;
  historyId: string | null;
  result: { urls?: string[]; url?: string } | null;
  error: string | null;
  creditEstimate: number;
  createdAt: string;
  updatedAt: string;
}

interface GenerationRow {
  id: string;
  account_id: string | null;
  mode: GenerationMode;
  model: string;
  prompt: string;
  params: string;
  status: GenerationStatus;
  history_id: string | null;
  result: string | null;
  error: string | null;
  created_at: string;
  updated_at: string;
}

export interface CreateGenerationInput {
  kind: GenerationKind;
  mode: GenerationMode;
  prompt: string;
  accountId?: string;
  strategy?: SelectionStrategy;
  modelId?: string;
  ratio?: string;
  resolution?: string;
  duration?: number;
  count?: number;
  sampleStrength?: number;
  negativePrompt?: string;
  referenceImages?: string[];
  firstFrame?: string;
  endFrame?: string;
}

/** 运行中的任务，用于支持取消 */
const running = new Map<string, AbortController>();

function parseJson<T>(raw: string | null, fallback: T): T {
  if (!raw) return fallback;
  try {
    return JSON.parse(raw) as T;
  } catch {
    return fallback;
  }
}

function toRecord(row: GenerationRow): GenerationRecord {
  const params = parseJson<Record<string, unknown>>(row.params, {});
  const isVideo = row.mode.endsWith("video");
  const modelLabel = isVideo
    ? (resolveVideoModel(row.model).label ?? row.model)
    : (resolveImageModel(row.model).label ?? row.model);

  return {
    id: row.id,
    accountId: row.account_id,
    accountName: (params.__accountName as string) ?? null,
    kind: isVideo ? "video" : "image",
    mode: row.mode,
    model: row.model,
    modelLabel,
    prompt: row.prompt,
    params,
    status: row.status,
    progress: (params.__progress as string) ?? null,
    historyId: row.history_id,
    result: parseJson<GenerationRecord["result"]>(row.result, null),
    error: row.error,
    creditEstimate: Number(params.__creditEstimate ?? 0),
    createdAt: row.created_at,
    updatedAt: row.updated_at
  };
}

export function listGenerations(limit = 50): GenerationRecord[] {
  const rows = db
    .prepare("SELECT * FROM generations ORDER BY created_at DESC LIMIT ?")
    .all(limit) as unknown as GenerationRow[];
  return rows.map(toRecord);
}

export function getGeneration(id: string): GenerationRecord {
  const row = db.prepare("SELECT * FROM generations WHERE id = ?").get(id) as unknown as
    | GenerationRow
    | undefined;
  if (!row) throw new AccountError(`生成记录不存在：${id}`, 404);
  return toRecord(row);
}

interface PatchFields {
  status?: GenerationStatus;
  historyId?: string | null;
  result?: string | null;
  error?: string | null;
  progress?: string | null;
  accountName?: string;
}

function patch(id: string, fields: PatchFields): void {
  const current = db.prepare("SELECT * FROM generations WHERE id = ?").get(id) as unknown as
    | GenerationRow
    | undefined;
  if (!current) return;

  const params = parseJson<Record<string, unknown>>(current.params, {});
  if (fields.progress !== undefined) params.__progress = fields.progress;
  if (fields.accountName) params.__accountName = fields.accountName;

  db.prepare(
    `UPDATE generations SET status = ?, history_id = ?, result = ?, error = ?, params = ?, updated_at = ? WHERE id = ?`
  ).run(
    nz(fields.status ?? current.status),
    nz(fields.historyId === undefined ? current.history_id : fields.historyId),
    nz(fields.result === undefined ? current.result : fields.result),
    nz(fields.error === undefined ? current.error : fields.error),
    JSON.stringify(params),
    nowIso(),
    id
  );
}

/**
 * 提交一次生成。立即返回 pending 记录，真正的生成在后台进行，
 * 前端通过轮询 /api/generations/:id 获取进度。
 */
export function createGeneration(input: CreateGenerationInput): GenerationRecord {
  if (!input.prompt?.trim() && input.mode !== "image2image") {
    throw new AccountError("提示词不能为空");
  }

  const picked: AccountCredential = selectAccount({
    accountId: input.accountId,
    strategy: input.strategy
  });

  const modelId = input.modelId ?? (input.kind === "video" ? "jimeng-video-seedance-2.0" : "jimeng-image-5.0-lite");
  const creditEstimate = estimateCredit(
    input.kind,
    modelId,
    input.count ?? 1,
    input.duration ?? undefined
  );

  const id = newId();
  const timestamp = nowIso();
  const params: Record<string, unknown> = {
    ratio: input.ratio,
    resolution: input.resolution,
    duration: input.duration,
    count: input.count ?? 1,
    sampleStrength: input.sampleStrength,
    negativePrompt: input.negativePrompt,
    references: input.referenceImages?.length ?? 0,
    hasFirstFrame: Boolean(input.firstFrame),
    __accountName: picked.name,
    __creditEstimate: creditEstimate
  };

  db.prepare(
    `INSERT INTO generations (id, account_id, mode, model, prompt, params, status, created_at, updated_at)
     VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?)`
  ).run(id, picked.id, input.mode, modelId, input.prompt ?? "", JSON.stringify(params), timestamp, timestamp);

  logEvent({
    accountId: picked.id,
    kind: "generate.submit",
    message: `提交${input.kind === "video" ? "视频" : "图片"}生成，使用账号「${picked.name}」`,
    detail: input.prompt.slice(0, 200)
  });

  void runGeneration(id, picked, input, modelId);
  return getGeneration(id);
}

async function runGeneration(
  id: string,
  picked: AccountCredential,
  input: CreateGenerationInput,
  modelId: string
): Promise<void> {
  const controller = new AbortController();
  running.set(id, controller);
  patch(id, { status: "running", progress: "已提交，等待即梦返回…" });

  const target = {
    accountId: picked.id,
    credential: picked.credential,
    credentialKind: picked.credentialKind,
    proxyUrl: picked.proxyUrl
  };

  const onProgress = (info: { attempt: number; status: number | null; phase: string }) => {
    patch(id, {
      progress:
        info.status === null
          ? `第 ${info.attempt} 次查询…`
          : `第 ${info.attempt} 次查询，状态码 ${info.status}`
    });
  };

  try {
    if (input.kind === "image") {
      const options: TextToImageOptions | ImageToImageOptions = {
        prompt: input.prompt,
        negativePrompt: input.negativePrompt,
        modelId,
        ratio: input.ratio,
        resolution: input.resolution,
        count: input.count ?? 1,
        sampleStrength: input.sampleStrength,
        ...(input.referenceImages?.length ? { referenceImages: input.referenceImages } : {})
      };
      const result = await generateImage(target, options, { onProgress, signal: controller.signal });
      patch(id, {
        status: "succeeded",
        historyId: result.historyId,
        result: JSON.stringify({ urls: result.urls }),
        error: null,
        progress: `完成，共 ${result.urls.length} 张`
      });
    } else {
      const options: VideoOptions = {
        prompt: input.prompt,
        modelId,
        ratio: input.ratio,
        resolution: input.resolution,
        duration: input.duration,
        firstFrame: input.firstFrame,
        endFrame: input.endFrame
      };
      const result = await generateVideo(target, options, { onProgress, signal: controller.signal });
      patch(id, {
        status: "succeeded",
        historyId: result.historyId,
        result: JSON.stringify({ url: result.url }),
        error: null,
        progress: `完成，时长 ${result.duration} 秒`
      });
    }

    markSuccess(picked.id);
    logEvent({
      accountId: picked.id,
      kind: "generate.success",
      message: "生成成功",
      detail: input.prompt.slice(0, 200)
    });
  } catch (error) {
    const aborted = controller.signal.aborted;
    const message = error instanceof Error ? error.message : String(error);

    patch(id, {
      status: aborted ? "canceled" : "failed",
      error: aborted ? "已取消" : message,
      progress: null
    });

    if (!aborted) {
      markFailure(picked.id, error);
      logEvent({
        accountId: picked.id,
        level: "error",
        kind: "generate.failed",
        message: `生成失败：${message}`,
        detail: input.prompt.slice(0, 200)
      });
    }
  } finally {
    running.delete(id);
  }
}

export function cancelGeneration(id: string): GenerationRecord {
  const controller = running.get(id);
  if (controller) controller.abort();
  return getGeneration(id);
}

/** 进程重启后，把残留在 running/pending 的任务标记为中断 */
export function reconcileInterruptedGenerations(): number {
  const result = db
    .prepare(
      `UPDATE generations SET status = 'failed', error = '服务重启，任务已中断', updated_at = ?
       WHERE status IN ('pending', 'running')`
    )
    .run(nowIso());
  return Number(result.changes ?? 0);
}

export interface GenerationStats {
  total: number;
  running: number;
  succeeded: number;
  failed: number;
  images: number;
  videos: number;
}

export function getGenerationStats(): GenerationStats {
  const row = db
    .prepare(
      `SELECT
         COUNT(*) AS total,
         SUM(CASE WHEN status IN ('pending','running') THEN 1 ELSE 0 END) AS running,
         SUM(CASE WHEN status = 'succeeded' THEN 1 ELSE 0 END) AS succeeded,
         SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) AS failed,
         SUM(CASE WHEN mode LIKE '%image%' THEN 1 ELSE 0 END) AS images,
         SUM(CASE WHEN mode LIKE '%video%' THEN 1 ELSE 0 END) AS videos
       FROM generations`
    )
    .get() as unknown as Record<string, number | null>;

  return {
    total: Number(row?.total ?? 0),
    running: Number(row?.running ?? 0),
    succeeded: Number(row?.succeeded ?? 0),
    failed: Number(row?.failed ?? 0),
    images: Number(row?.images ?? 0),
    videos: Number(row?.videos ?? 0)
  };
}

export { JimengError };
