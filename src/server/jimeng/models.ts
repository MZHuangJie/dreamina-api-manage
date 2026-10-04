/** 即梦网页端的模型目录与参数枚举（对齐官网前端） */

export interface ImageModel {
  id: string;
  /** 上游请求体里的 model / model_req_key */
  reqKey: string;
  label: string;
  resolutions: string[];
  defaultResolution: string;
  /** 每张图的积分消耗，用于生成前预估 */
  creditPerImage: number;
}

export const IMAGE_MODELS: ImageModel[] = [
  { id: "jimeng-image-5.0-pro", reqKey: "high_aes_general_v50p_large", label: "Seedream 5.0 Pro", resolutions: ["4k", "2k", "1.5k"], defaultResolution: "2k", creditPerImage: 8 },
  { id: "jimeng-image-5.0-lite", reqKey: "high_aes_general_v50", label: "Seedream 5.0 Lite", resolutions: ["4k", "2k"], defaultResolution: "2k", creditPerImage: 3 },
  { id: "jimeng-image-4.7", reqKey: "high_aes_general_v43", label: "Seedream 4.7", resolutions: ["4k", "2k"], defaultResolution: "2k", creditPerImage: 4 },
  { id: "jimeng-image-4.6", reqKey: "high_aes_general_v42", label: "Seedream 4.6", resolutions: ["4k", "2k"], defaultResolution: "2k", creditPerImage: 4 },
  { id: "jimeng-image-4.5", reqKey: "high_aes_general_v40l", label: "Seedream 4.5", resolutions: ["4k", "2k"], defaultResolution: "2k", creditPerImage: 4 },
  { id: "jimeng-image-4.1", reqKey: "high_aes_general_v41", label: "Seedream 4.1", resolutions: ["4k", "2k"], defaultResolution: "2k", creditPerImage: 4 },
  { id: "jimeng-image-4.0", reqKey: "high_aes_general_v40", label: "Seedream 4.0", resolutions: ["4k", "2k"], defaultResolution: "2k", creditPerImage: 4 },
  { id: "jimeng-image-3.1", reqKey: "high_aes_general_v30l_art_fangzhou:general_v3.0_18b", label: "Seedream 3.1", resolutions: ["1k"], defaultResolution: "1k", creditPerImage: 1 },
  { id: "jimeng-image-3.0", reqKey: "high_aes_general_v30l:general_v3.0_18b", label: "Seedream 3.0", resolutions: ["1k"], defaultResolution: "1k", creditPerImage: 1 },
  { id: "jimeng-image-2.0-pro", reqKey: "high_aes_general_v20_L:general_v2.0_L", label: "Seedream 2.0 Pro", resolutions: ["1k"], defaultResolution: "1k", creditPerImage: 1 }
];

export interface VideoModel {
  id: string;
  reqKey: string;
  label: string;
  resolutions: string[];
  defaultResolution: string;
  durations: number[];
  defaultDuration: number;
  /** 视频计费按「积分/秒」，用于生成前预估 */
  creditPerSecond: number;
  /** extend.m_video_commerce_info.benefit_type 按分辨率区分 */
  benefits: Record<string, string>;
  defaultBenefit: string;
}

const SEEDANCE_2_DURATIONS = [4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15];

export const VIDEO_MODELS: VideoModel[] = [
  {
    id: "jimeng-video-seedance-2.5", reqKey: "dreamina_seedance_45_pro", label: "Seedance 2.5 Pro",
    resolutions: ["720p"], defaultResolution: "720p", durations: [5, 10], defaultDuration: 5,
    creditPerSecond: 12, benefits: { "720p": "dreamina_video_seedance_45_pro" }, defaultBenefit: "dreamina_video_seedance_45_pro"
  },
  {
    id: "jimeng-video-seedance-2.0", reqKey: "dreamina_seedance_40_pro", label: "Seedance 2.0 Pro",
    resolutions: ["720p"], defaultResolution: "720p", durations: SEEDANCE_2_DURATIONS, defaultDuration: 5,
    creditPerSecond: 10, benefits: { "720p": "dreamina_video_seedance_20_pro" }, defaultBenefit: "dreamina_video_seedance_20_pro"
  },
  {
    id: "jimeng-video-seedance-2.0-fast", reqKey: "dreamina_seedance_40", label: "Seedance 2.0 Fast",
    resolutions: ["720p"], defaultResolution: "720p", durations: SEEDANCE_2_DURATIONS, defaultDuration: 5,
    creditPerSecond: 6, benefits: { "720p": "dreamina_video_seedance_20" }, defaultBenefit: "dreamina_video_seedance_20"
  },
  {
    id: "jimeng-video-seedance-2.0-mini", reqKey: "dreamina_seedance_40_mini", label: "Seedance 2.0 Mini",
    resolutions: ["720p"], defaultResolution: "720p", durations: SEEDANCE_2_DURATIONS, defaultDuration: 5,
    creditPerSecond: 3, benefits: { "720p": "dreamina_video_seedance_20_mini" }, defaultBenefit: "dreamina_video_seedance_20_mini"
  },
  {
    id: "jimeng-video-seedance-2.0-vip", reqKey: "dreamina_seedance_40_pro_vision", label: "Seedance 2.0 Pro VIP",
    resolutions: ["4k", "1080p", "720p"], defaultResolution: "720p", durations: SEEDANCE_2_DURATIONS, defaultDuration: 5,
    creditPerSecond: 10,
    benefits: { "720p": "seedance_20_pro_720p_output", "1080p": "seedance_20_pro_1080p_output", "4k": "seedance_20_pro_4k_output" },
    defaultBenefit: "dreamina_video_seedance_20_pro"
  },
  {
    id: "jimeng-video-3.0-pro", reqKey: "dreamina_ic_generate_video_model_vgfm_3.0_pro", label: "视频 3.0 Pro",
    resolutions: ["1080p"], defaultResolution: "1080p", durations: [5, 10], defaultDuration: 5,
    creditPerSecond: 10, benefits: { "1080p": "basic_video_operation_vgfm_v_three_pro" }, defaultBenefit: "basic_video_operation_vgfm_v_three_pro"
  },
  {
    id: "jimeng-video-3.0", reqKey: "dreamina_ic_generate_video_model_vgfm_3.0", label: "视频 3.0",
    resolutions: ["720p"], defaultResolution: "720p", durations: [5, 10], defaultDuration: 5,
    creditPerSecond: 6, benefits: { "720p": "basic_video_operation_vgfm_v_three" }, defaultBenefit: "basic_video_operation_vgfm_v_three"
  },
  {
    id: "jimeng-video-3.0-fast", reqKey: "dreamina_ic_generate_video_model_vgfm_3.0_fast", label: "视频 3.0 Fast",
    resolutions: ["1080p", "720p"], defaultResolution: "720p", durations: [5, 10], defaultDuration: 5,
    creditPerSecond: 4, benefits: { "720p": "basic_video_operation_vgfm_v_three_fast", "1080p": "basic_video_operation_vgfm_v_three_fast" },
    defaultBenefit: "basic_video_operation_vgfm_v_three_fast"
  }
];

/* ------------------------------ 画面比例 ------------------------------ */

export const IMAGE_RATIOS = ["21:9", "16:9", "3:2", "4:3", "1:1", "3:4", "2:3", "9:16"] as const;
export type ImageRatio = (typeof IMAGE_RATIOS)[number];

/** 官网的 image_ratio 是枚举数字，注意 1:1 是 8 而不是 4 */
export const IMAGE_RATIO_CODE: Record<string, number> = {
  "21:9": 0, "16:9": 1, "3:2": 2, "4:3": 3, "1:1": 8, "3:4": 4, "2:3": 5, "9:16": 6
};

export const VIDEO_RATIOS = ["16:9", "9:16", "1:1", "4:3", "3:4", "21:9"] as const;

/** 分辨率 → 实际像素尺寸 */
const DIMENSIONS: Record<string, Record<string, [number, number]>> = {
  "21:9": { "1k": [2016, 846], "1.5k": [2268, 972], "2k": [3024, 1296], "4k": [6197, 2656] },
  "16:9": { "1k": [1664, 936], "1.5k": [1920, 1080], "2k": [2560, 1440], "4k": [5404, 3040] },
  "3:2": { "1k": [1584, 1056], "1.5k": [1872, 1248], "2k": [2496, 1664], "4k": [4992, 3328] },
  "4:3": { "1k": [1472, 1104], "1.5k": [1728, 1296], "2k": [2304, 1728], "4k": [4693, 3520] },
  "1:1": { "1k": [1328, 1328], "1.5k": [1536, 1536], "2k": [2048, 2048], "4k": [4096, 4096] },
  "3:4": { "1k": [1104, 1472], "1.5k": [1296, 1728], "2k": [1728, 2304], "4k": [3520, 4693] },
  "2:3": { "1k": [1056, 1584], "1.5k": [1248, 1872], "2k": [1664, 2496], "4k": [3328, 4992] },
  "9:16": { "1k": [936, 1664], "1.5k": [1080, 1920], "2k": [1440, 2560], "4k": [3040, 5404] }
};

export function imageDimensions(ratio: string, resolution: string): { width: number; height: number } {
  const table = DIMENSIONS[ratio] ?? DIMENSIONS["1:1"]!;
  const [width, height] = table[resolution] ?? table["2k"] ?? [2048, 2048];
  return { width, height };
}

/* ------------------------------ 解析 ------------------------------ */

export function resolveImageModel(id?: string | null): ImageModel {
  return IMAGE_MODELS.find((m) => m.id === id) ?? IMAGE_MODELS.find((m) => m.id === "jimeng-image-5.0-lite")!;
}

export function resolveVideoModel(id?: string | null): VideoModel {
  return VIDEO_MODELS.find((m) => m.id === id) ?? VIDEO_MODELS.find((m) => m.id === "jimeng-video-seedance-2.0")!;
}

export function normalizeImageRatio(ratio?: string | null, prompt = ""): ImageRatio {
  if (ratio && IMAGE_RATIOS.includes(ratio as ImageRatio)) return ratio as ImageRatio;
  if (/横屏|横版|宽屏/.test(prompt)) return "16:9";
  if (/竖屏|竖版|手机/.test(prompt)) return "9:16";
  if (/方形|正方/.test(prompt)) return "1:1";
  const match = prompt.match(/(\d+)\s*[:：]\s*(\d+)/);
  if (match) {
    const candidate = `${match[1]}:${match[2]}`;
    if (IMAGE_RATIOS.includes(candidate as ImageRatio)) return candidate as ImageRatio;
  }
  return "1:1";
}

export function normalizeVideoRatio(ratio?: string | null, prompt = ""): string {
  if (ratio && VIDEO_RATIOS.includes(ratio as (typeof VIDEO_RATIOS)[number])) return ratio;
  if (/横屏|横版|宽屏/.test(prompt)) return "16:9";
  if (/竖屏|竖版|手机/.test(prompt)) return "9:16";
  if (/方形|正方/.test(prompt)) return "1:1";
  return "16:9";
}

export function normalizeResolution<T extends { resolutions: string[]; defaultResolution: string }>(
  model: T,
  requested?: string | null
): string {
  if (requested && model.resolutions.includes(requested)) return requested;
  return model.defaultResolution;
}

export function normalizeVideoDuration(model: VideoModel, requested?: number | null): number {
  if (typeof requested === "number" && model.durations.includes(requested)) return requested;
  return model.defaultDuration;
}

export function estimateCredit(
  kind: "image" | "video",
  modelId: string,
  count: number,
  duration?: number
): number {
  if (kind === "image") return resolveImageModel(modelId).creditPerImage * Math.max(1, count);
  return resolveVideoModel(modelId).creditPerSecond * (duration ?? resolveVideoModel(modelId).defaultDuration);
}
