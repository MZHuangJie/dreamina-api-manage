import { Hono } from "hono";
import { z } from "zod";

import { db } from "../db.ts";
import { fail, ok, readJson } from "../http.ts";
import {
  IMAGE_MODELS,
  IMAGE_RATIOS,
  VIDEO_MODELS,
  VIDEO_RATIOS
} from "../jimeng/models.ts";
import {
  cancelGeneration,
  createGeneration,
  getGeneration,
  getGenerationStats,
  listGenerations
} from "../generate/tasks.ts";

const strategySchema = z.enum(["active", "least_failures", "round_robin", "most_credit"]).optional();

const createSchema = z
  .object({
    kind: z.enum(["image", "video"]),
    mode: z.enum(["text2image", "image2image", "text2video", "image2video"]),
    prompt: z.string().max(4000).default(""),
    accountId: z.string().optional(),
    strategy: strategySchema,
    modelId: z.string().max(80).optional(),
    ratio: z.string().max(12).optional(),
    resolution: z.string().max(12).optional(),
    duration: z.number().int().min(1).max(60).optional(),
    count: z.number().int().min(1).max(8).optional(),
    sampleStrength: z.number().min(0).max(1).optional(),
    negativePrompt: z.string().max(2000).optional(),
    referenceImages: z.array(z.string().max(200_000)).max(10).optional(),
    firstFrame: z.string().max(200_000).optional(),
    endFrame: z.string().max(200_000).optional()
  })
  .refine((value) => value.mode !== "image2image" || (value.referenceImages?.length ?? 0) > 0, {
    message: "图生图至少需要一张参考图"
  })
  .refine((value) => value.mode !== "image2video" || Boolean(value.firstFrame), {
    message: "图生视频需要提供首帧图"
  });

export const generateRoutes = new Hono();

/** 模型目录，供前端渲染下拉框 */
generateRoutes.get("/models", (c) =>
  ok(c, {
    image: IMAGE_MODELS,
    video: VIDEO_MODELS,
    imageRatios: IMAGE_RATIOS,
    videoRatios: VIDEO_RATIOS
  })
);

generateRoutes.post("/generate", async (c) => {
  try {
    const input = await readJson(c, createSchema);
    const record = createGeneration(input);
    return ok(c, record, 202);
  } catch (error) {
    return fail(c, error);
  }
});

generateRoutes.get("/generations/stats", (c) => ok(c, getGenerationStats()));

generateRoutes.get("/generations", (c) => {
  const limit = Math.min(Number(c.req.query("limit") ?? 50) || 50, 200);
  return ok(c, listGenerations(limit));
});

generateRoutes.get("/generations/:id", (c) => {
  try {
    return ok(c, getGeneration(c.req.param("id")));
  } catch (error) {
    return fail(c, error);
  }
});

generateRoutes.post("/generations/:id/cancel", (c) => {
  try {
    return ok(c, cancelGeneration(c.req.param("id")));
  } catch (error) {
    return fail(c, error);
  }
});

generateRoutes.delete("/generations/:id", (c) => {
  try {
    const record = getGeneration(c.req.param("id"));
    cancelGeneration(record.id);
    db.prepare("DELETE FROM generations WHERE id = ?").run(record.id);
    return ok(c, { deleted: true });
  } catch (error) {
    return fail(c, error);
  }
});
