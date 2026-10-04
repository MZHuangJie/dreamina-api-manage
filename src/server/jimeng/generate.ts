import crypto from "node:crypto";

import { config } from "../config.ts";
import { jimengRequest } from "./client.ts";
import { JimengCreditError, JimengError } from "./errors.ts";
import {
  imageDimensions,
  normalizeImageRatio,
  normalizeResolution,
  normalizeVideoDuration,
  normalizeVideoRatio,
  resolveImageModel,
  resolveVideoModel
} from "./models.ts";
import type { ImageModel, VideoModel } from "./models.ts";
import { loadImageBytes, uploadImage } from "./upload.ts";

/** 与官网前端对齐的版本号 */
const DRAFT_VERSION = "3.3.20";
const WEB_VERSION = "7.5.0";
const DRAFT_CONTENT_VERSION = "3.0.2";
const VIDEO_DRAFT_MIN_VERSION = "3.0.5";
const VIDEO_DRAFT_VERSION = "3.3.20";
const IMAGE_COMPONENT_MIN_VERSION = "3.0.2";

const IMAGE_PROCESSING_STATES = new Set([20, 42, 45]);
const FAIL_STATE = 30;
const CONTENT_FILTERED_CODE = "2038";

const uuid = (): string => crypto.randomUUID();
const randomSeed = (): number => Math.floor(Math.random() * 100_000_000) + 2_500_000_000;

export interface GenerateTarget {
  accountId: string;
  credential: string;
  credentialKind: "sessionid" | "cookie";
  proxyUrl?: string | null;
}

const imageRequestParams = () => ({
  aid: config.jimeng.appid,
  da_version: DRAFT_VERSION,
  web_component_open_flag: 1,
  web_version: WEB_VERSION
});

const videoRequestParams = () => ({
  aid: config.jimeng.appid,
  aigc_features: "app_lip_sync",
  web_version: WEB_VERSION,
  da_version: DRAFT_VERSION,
  web_component_open_flag: 1
});

/* ------------------------------------------------------------------ */
/* 请求体构造                                                          */
/* ------------------------------------------------------------------ */

function imageSceneOptions(model: ImageModel, resolution: string, count: number): string {
  return JSON.stringify([
    {
      type: "image",
      scene: "ImageBasicGenerate",
      modelReqKey: model.reqKey,
      resolutionType: resolution,
      abilityList: [],
      benefitCount: count,
      reportParams: {
        enterSource: "generate",
        vipSource: "generate",
        extraVipFunctionKey: `${model.reqKey}-${resolution}`,
        useVipFunctionDetailsReporterHoc: true
      }
    }
  ]);
}

function buildMetricsExtra(model: ImageModel, resolution: string, count: number, generateId: string): string {
  return JSON.stringify({
    promptSource: "custom",
    generateCount: count,
    enterFrom: "click",
    sceneOptions: imageSceneOptions(model, resolution, count),
    isBoxSelect: false,
    isCutout: false,
    generateId,
    isRegenerate: false
  });
}

export interface TextToImageOptions {
  prompt: string;
  negativePrompt?: string;
  modelId?: string;
  ratio?: string;
  resolution?: string;
  count?: number;
  sampleStrength?: number;
  seed?: number;
}

/** 文生图草稿体 */
function buildTextToImageDraft(options: TextToImageOptions): {
  draftContent: string;
  metricsExtra: string;
  model: ImageModel;
  resolution: string;
  ratio: string;
  width: number;
  height: number;
  count: number;
} {
  const model = resolveImageModel(options.modelId);
  const ratio = normalizeImageRatio(options.ratio, options.prompt);
  const resolution = normalizeResolution(model, options.resolution);
  const { width, height } = imageDimensions(ratio, resolution);
  const count = Math.min(Math.max(options.count ?? 1, 1), 8);
  const seed = options.seed ?? randomSeed();
  const componentId = uuid();

  const draftContent = JSON.stringify({
    type: "draft",
    id: uuid(),
    min_version: DRAFT_CONTENT_VERSION,
    min_features: [],
    is_from_tsn: true,
    version: DRAFT_CONTENT_VERSION,
    main_component_id: componentId,
    component_list: [
      {
        type: "image_base_component",
        id: componentId,
        min_version: IMAGE_COMPONENT_MIN_VERSION,
        metadata: {
          type: "",
          id: uuid(),
          created_platform: 3,
          created_platform_version: "",
          created_time_in_ms: String(Date.now()),
          created_did: ""
        },
        generate_type: "generate",
        aigc_mode: "workbench",
        gen_type: 1,
        abilities: {
          type: "",
          id: uuid(),
          generate: {
            type: "",
            id: uuid(),
            core_param: {
              type: "",
              id: uuid(),
              model: model.reqKey,
              prompt: options.prompt,
              negative_prompt: options.negativePrompt ?? "",
              seed,
              sample_strength: options.sampleStrength ?? 0.5,
              image_ratio: IMAGE_RATIO_CODE_LOCAL[ratio] ?? 8,
              large_image_info: { type: "", id: uuid(), height, width, resolution_type: resolution },
              generate_type: 0
            },
            history_option: { type: "", id: uuid() }
          },
          gen_option: { type: "", id: uuid(), gen_count: count, generate_all: false }
        }
      }
    ]
  });

  return {
    draftContent,
    metricsExtra: buildMetricsExtra(model, resolution, count, uuid()),
    model,
    resolution,
    ratio,
    width,
    height,
    count
  };
}

const IMAGE_RATIO_CODE_LOCAL: Record<string, number> = {
  "21:9": 0, "16:9": 1, "3:2": 2, "4:3": 3, "1:1": 8, "3:4": 4, "2:3": 5, "9:16": 6
};

export interface ImageToImageOptions extends TextToImageOptions {
  /** dataURL / 网络 URL / 本地路径 */
  referenceImages: string[];
}

/** 图生图（参考图 / 混合模式）草稿体 */
function buildImageToImageDraft(
  options: ImageToImageOptions,
  imageUris: string[]
): { draftContent: string; model: ImageModel; resolution: string; ratio: string; width: number; height: number; count: number } {
  const model = resolveImageModel(options.modelId);
  const ratio = normalizeImageRatio(options.ratio, options.prompt);
  const resolution = normalizeResolution(model, options.resolution);
  const { width, height } = imageDimensions(ratio, resolution);
  const count = Math.min(Math.max(options.count ?? 1, 1), 8);
  const componentId = uuid();

  const draftContent = JSON.stringify({
    type: "draft",
    id: uuid(),
    min_version: DRAFT_CONTENT_VERSION,
    min_features: [],
    is_from_tsn: true,
    version: DRAFT_CONTENT_VERSION,
    main_component_id: componentId,
    component_list: [
      {
        type: "image_base_component",
        id: componentId,
        min_version: IMAGE_COMPONENT_MIN_VERSION,
        metadata: {
          type: "",
          id: uuid(),
          created_platform: 3,
          created_platform_version: "",
          created_time_in_ms: String(Date.now()),
          created_did: ""
        },
        generate_type: "blend",
        aigc_mode: "workbench",
        gen_type: 1,
        abilities: {
          type: "",
          id: uuid(),
          blend: {
            type: "",
            id: uuid(),
            min_features: [],
            core_param: {
              type: "",
              id: uuid(),
              model: model.reqKey,
              // 混合模式会在提示词末尾追加 ##，这是官网前端的既定行为
              prompt: `${options.prompt}##`,
              sample_strength: options.sampleStrength ?? 0.5,
              image_ratio: IMAGE_RATIO_CODE_LOCAL[ratio] ?? 8,
              large_image_info: { type: "", id: uuid(), height, width, resolution_type: resolution }
            },
            ability_list: [
              {
                type: "",
                id: uuid(),
                name: "byte_edit",
                image_uri_list: imageUris,
                image_list: imageUris.map((uri) => ({
                  type: "image",
                  id: uuid(),
                  source_from: "upload",
                  platform_type: 1,
                  name: "",
                  image_uri: uri,
                  width: 0,
                  height: 0,
                  format: "",
                  uri
                })),
                strength: 0.5
              }
            ],
            history_option: { type: "", id: uuid() },
            prompt_placeholder_info_list: [{ type: "", id: uuid(), ability_index: 0 }],
            postedit_param: { type: "", id: uuid(), generate_type: 0 }
          },
          gen_option: { type: "", id: uuid(), gen_count: count, generate_all: false }
        }
      }
    ]
  });

  return { draftContent, model, resolution, ratio, width, height, count };
}

export interface VideoOptions {
  prompt: string;
  modelId?: string;
  ratio?: string;
  resolution?: string;
  duration?: number;
  seed?: number;
  /** 首帧图：给了就走图生视频 */
  firstFrame?: string;
  endFrame?: string;
}

function buildVideoDraft(
  options: VideoOptions,
  firstFrameUri: string | null,
  endFrameUri: string | null
): { draftContent: string; metricsExtra: string; model: VideoModel; resolution: string; ratio: string; duration: number } {
  const model = resolveVideoModel(options.modelId);
  const ratio = normalizeVideoRatio(options.ratio, options.prompt);
  const resolution = normalizeResolution(model, options.resolution);
  const duration = normalizeVideoDuration(model, options.duration);
  const seed = options.seed ?? randomSeed();
  const componentId = uuid();
  const submitId = uuid();

  // video_mode: 0 = 带首帧/尾帧，2 = 纯文生视频
  const videoMode = firstFrameUri || endFrameUri ? 0 : 2;

  const metricsExtra = JSON.stringify({
    enterFrom: "click",
    isDefaultSeed: 1,
    promptSource: "custom",
    isRegenerate: false,
    originSubmitId: submitId
  });

  const frameImage = (uri: string | null) =>
    uri ? { type: "image", id: uuid(), source_from: "upload", platform_type: 1, name: "", image_uri: uri, uri, width: 0, height: 0, format: "" } : null;

  const draftContent = JSON.stringify({
    type: "draft",
    id: uuid(),
    min_version: VIDEO_DRAFT_MIN_VERSION,
    is_from_tsn: true,
    version: VIDEO_DRAFT_VERSION,
    main_component_id: componentId,
    component_list: [
      {
        type: "video_base_component",
        id: componentId,
        min_version: "1.0.0",
        metadata: {
          type: "",
          id: uuid(),
          created_platform: 3,
          created_platform_version: "",
          // 注意：视频路径这里是数字，图片路径是字符串
          created_time_in_ms: Date.now(),
          created_did: ""
        },
        generate_type: "gen_video",
        aigc_mode: "workbench",
        abilities: {
          type: "",
          id: uuid(),
          gen_video: {
            id: uuid(),
            type: "",
            text_to_video_params: {
              type: "",
              id: uuid(),
              model_req_key: model.reqKey,
              priority: 0,
              seed,
              video_aspect_ratio: ratio,
              video_gen_inputs: [
                {
                  duration_ms: duration * 1000,
                  first_frame_image: frameImage(firstFrameUri),
                  end_frame_image: frameImage(endFrameUri),
                  fps: 24,
                  id: uuid(),
                  min_version: VIDEO_DRAFT_MIN_VERSION,
                  prompt: options.prompt,
                  resolution,
                  type: "",
                  video_mode: videoMode
                }
              ]
            },
            video_task_extra: metricsExtra
          }
        }
      }
    ]
  });

  return { draftContent, metricsExtra, model, resolution, ratio, duration };
}

function videoCommerceInfo(model: VideoModel, resolution: string) {
  return {
    benefit_type: model.benefits[resolution] ?? model.defaultBenefit,
    resource_id: "generate_video",
    resource_id_type: "str",
    resource_sub_type: "aigc"
  };
}

/* ------------------------------------------------------------------ */
/* 提交                                                                */
/* ------------------------------------------------------------------ */

interface SubmitResult {
  historyId: string;
  raw: unknown;
}

async function submitDraft(
  target: GenerateTarget,
  params: Record<string, string | number>,
  body: Record<string, unknown>
): Promise<SubmitResult> {
  const data = await jimengRequest<{ aigc_data?: { history_record_id?: string } }>({
    accountId: target.accountId,
    uri: "/mweb/v1/aigc_draft/generate",
    credential: target.credential,
    credentialKind: target.credentialKind,
    params,
    data: body,
    proxyUrl: target.proxyUrl
  });

  const historyId = data?.aigc_data?.history_record_id;
  if (!historyId) throw new JimengError("即梦未返回生成记录 ID，提交可能被拒绝");
  return { historyId, raw: data };
}

/* ------------------------------------------------------------------ */
/* 轮询                                                                */
/* ------------------------------------------------------------------ */

const IMAGE_INFO = {
  width: 2048,
  height: 2048,
  format: "webp",
  image_scene_list: [
    { scene: "smart_crop", width: 360, height: 360, uniq_key: "smart_crop-w:360-h:360", format: "webp" },
    { scene: "smart_crop", width: 480, height: 480, uniq_key: "smart_crop-w:480-h:480", format: "webp" },
    { scene: "smart_crop", width: 720, height: 720, uniq_key: "smart_crop-w:720-h:720", format: "webp" },
    { scene: "normal", width: 2400, height: 2400, uniq_key: "2400", format: "webp" },
    { scene: "normal", width: 1080, height: 1080, uniq_key: "1080", format: "webp" },
    { scene: "normal", width: 720, height: 720, uniq_key: "720", format: "webp" }
  ]
};

interface HistoryRecord {
  status?: number;
  fail_code?: string | number;
  item_list?: unknown[];
}

function extractImageUrls(items: unknown[]): string[] {
  return items
    .map((item) => {
      const record = item as {
        image?: { large_images?: { image_url?: string }[] };
        common_attr?: { cover_url?: string };
      };
      return record?.image?.large_images?.[0]?.image_url ?? record?.common_attr?.cover_url ?? null;
    })
    .filter((url): url is string => Boolean(url));
}

function extractVideoUrl(items: unknown[]): string | null {
  for (const item of items) {
    const record = item as {
      video?: {
        transcoded_video?: { origin?: { video_url?: string } };
        play_url?: string;
        download_url?: string;
        url?: string;
      };
    };
    const video = record?.video;
    const url =
      video?.transcoded_video?.origin?.video_url ??
      video?.play_url ??
      video?.download_url ??
      video?.url;
    if (url) return url;
  }
  // 兜底：整段 payload 里正则捞一个视频地址
  const text = JSON.stringify(items);
  const match = text.match(/https?:\/\/[^"']*?(?:vlabvod|\.mp4)[^"']*/);
  return match ? match[0] : null;
}

export interface PollOptions {
  timeoutMs?: number;
  intervalMs?: number;
  onProgress?: (info: { attempt: number; status: number | null; phase: string }) => void;
  signal?: AbortSignal;
}

function isProcessing(status: number | undefined): boolean {
  return status === undefined || IMAGE_PROCESSING_STATES.has(status);
}

function failMessage(record: HistoryRecord): string {
  const code = String(record.fail_code ?? "");
  if (code === CONTENT_FILTERED_CODE) return "生成失败：内容未通过平台审核（违规过滤）";
  return `生成失败（状态码 ${record.status}${code ? `, 失败码 ${code}` : ""}）`;
}

/** 轮询图片结果 */
export async function pollImage(
  target: GenerateTarget,
  historyId: string,
  options: PollOptions = {}
): Promise<string[]> {
  const timeoutMs = options.timeoutMs ?? 180_000;
  const intervalMs = options.intervalMs ?? 1_000;
  const deadline = Date.now() + timeoutMs;
  let attempt = 0;

  while (Date.now() < deadline) {
    if (options.signal?.aborted) throw new JimengError("已取消");
    attempt += 1;

    const result = await jimengRequest<Record<string, HistoryRecord>>({
      accountId: target.accountId,
      uri: "/mweb/v1/get_history_by_ids",
      credential: target.credential,
      credentialKind: target.credentialKind,
      data: { history_ids: [historyId], image_info: IMAGE_INFO, http_common_info: { aid: Number(config.jimeng.appid) } },
      proxyUrl: target.proxyUrl,
      retries: 1
    });

    const record = result?.[historyId];
    if (!record) throw new JimengError("即梦查不到该生成记录，可能已被删除");

    options.onProgress?.({ attempt, status: record.status ?? null, phase: "image" });

    if (record.status === FAIL_STATE) throw new JimengError(failMessage(record));
    if (!isProcessing(record.status) && (record.item_list?.length ?? 0) > 0) {
      const urls = extractImageUrls(record.item_list ?? []);
      if (urls.length) return urls;
    }

    await new Promise((resolve) => setTimeout(resolve, intervalMs));
  }

  throw new JimengError(`图片生成超时（已等待 ${Math.round(timeoutMs / 1000)} 秒）`);
}

/** 轮询视频结果：主用 get_history_by_ids，多次无果后切到 get_history_records */
export async function pollVideo(
  target: GenerateTarget,
  historyId: string,
  options: PollOptions = {}
): Promise<string> {
  const timeoutMs = options.timeoutMs ?? 20 * 60_000;
  const intervalMs = options.intervalMs ?? 3_000;
  const deadline = Date.now() + timeoutMs;
  let attempt = 0;

  while (Date.now() < deadline) {
    if (options.signal?.aborted) throw new JimengError("已取消");
    attempt += 1;

    const useRecordsEndpoint = attempt > 10 && attempt % 2 === 0;
    let record: HistoryRecord | undefined;

    if (useRecordsEndpoint) {
      const result = await jimengRequest<Record<string, HistoryRecord>>({
        accountId: target.accountId,
        uri: "/mweb/v1/get_history_records",
        credential: target.credential,
        credentialKind: target.credentialKind,
        data: { history_record_ids: [historyId] },
        proxyUrl: target.proxyUrl,
        retries: 1
      });
      record = result?.[historyId] ?? Object.values(result ?? {})[0];
    } else {
      const result = await jimengRequest<Record<string, HistoryRecord>>({
        accountId: target.accountId,
        uri: "/mweb/v1/get_history_by_ids",
        credential: target.credential,
        credentialKind: target.credentialKind,
        data: { history_ids: [historyId] },
        proxyUrl: target.proxyUrl,
        retries: 1
      });
      record = result?.[historyId] ?? Object.values(result ?? {})[0];
    }

    options.onProgress?.({ attempt, status: record?.status ?? null, phase: "video" });

    if (record) {
      if (record.status === FAIL_STATE) throw new JimengError(failMessage(record));
      const url = extractVideoUrl(record.item_list ?? []);
      if (url) return url;
    }

    await new Promise((resolve) => setTimeout(resolve, intervalMs));
  }

  throw new JimengError(`视频生成超时（已等待 ${Math.round(timeoutMs / 60000)} 分钟），可稍后到即梦官网查看`);
}

/* ------------------------------------------------------------------ */
/* 对外主流程                                                          */
/* ------------------------------------------------------------------ */

export interface ImageResult {
  historyId: string;
  urls: string[];
  model: ImageModel;
  resolution: string;
  ratio: string;
  count: number;
  creditEstimate: number;
}

export async function generateImage(
  target: GenerateTarget,
  options: TextToImageOptions | ImageToImageOptions,
  poll: PollOptions = {}
): Promise<ImageResult> {
  const references = (options as ImageToImageOptions).referenceImages ?? [];
  const useBlend = references.length > 0;

  let imageUris: string[] = [];
  if (useBlend) {
    imageUris = [];
    for (const source of references.slice(0, 10)) {
      const bytes = await loadImageBytes(source);
      imageUris.push(await uploadImage(target, bytes));
    }
  }

  const draft = useBlend
    ? buildImageToImageDraft(options as ImageToImageOptions, imageUris)
    : buildTextToImageDraft(options);

  const body: Record<string, unknown> = {
    extend: { root_model: draft.model.reqKey },
    submit_id: uuid(),
    draft_content: draft.draftContent,
    http_common_info: { aid: Number(config.jimeng.appid) }
  };
  // 混合模式不带 metrics_extra，这是官网前端的既定差异
  if (!useBlend) {
    body.metrics_extra = (draft as ReturnType<typeof buildTextToImageDraft>).metricsExtra;
  }

  const { historyId } = await submitDraft(target, imageRequestParams(), body);
  const urls = await pollImage(target, historyId, poll);

  return {
    historyId,
    urls,
    model: draft.model,
    resolution: draft.resolution,
    ratio: draft.ratio,
    count: draft.count,
    creditEstimate: draft.model.creditPerImage * draft.count
  };
}

export interface VideoResult {
  historyId: string;
  url: string;
  model: VideoModel;
  resolution: string;
  ratio: string;
  duration: number;
}

export async function generateVideo(target: GenerateTarget, options: VideoOptions, poll: PollOptions = {}): Promise<VideoResult> {
  let firstFrameUri: string | null = null;
  let endFrameUri: string | null = null;

  if (options.firstFrame) {
    firstFrameUri = await uploadImage(target, await loadImageBytes(options.firstFrame), { forVideo: true });
  }
  if (options.endFrame) {
    endFrameUri = await uploadImage(target, await loadImageBytes(options.endFrame), { forVideo: true });
  }

  const draft = buildVideoDraft(options, firstFrameUri, endFrameUri);
  const commerce = videoCommerceInfo(draft.model, draft.resolution);

  const body = {
    extend: {
      root_model: draft.model.reqKey,
      m_video_commerce_info: commerce,
      m_video_commerce_info_list: [commerce]
    },
    submit_id: uuid(),
    metrics_extra: draft.metricsExtra,
    draft_content: draft.draftContent,
    http_common_info: { aid: Number(config.jimeng.appid) }
  };

  const { historyId } = await submitDraft(target, videoRequestParams(), body);
  const url = await pollVideo(target, historyId, poll);

  return {
    historyId,
    url,
    model: draft.model,
    resolution: draft.resolution,
    ratio: draft.ratio,
    duration: draft.duration
  };
}

export { JimengCreditError, JimengError };
