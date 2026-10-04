import crypto from "node:crypto";
import { ProxyAgent, request as undiciRequest } from "undici";

import { config } from "../config.ts";
import { extractSessionId } from "../crypto.ts";
import {
  JimengAuthError,
  JimengError,
  JimengNetworkError,
  JimengProxyError,
  classifyRet
} from "./errors.ts";
import type { JimengEnvelope } from "./errors.ts";

/**
 * 即梦网页端签名算法。
 * 服务端会校验 Sign 与 Device-Time 是否匹配，因此每个请求都要现算。
 */
export function buildSign(uri: string, deviceTime: number): string {
  const raw = `9e2c|${uri.slice(-7)}|${config.jimeng.platformCode}|${config.jimeng.versionCode}|${deviceTime}||11ac`;
  return crypto.createHash("md5").update(raw, "utf8").digest("hex");
}

/** 从账号 id 派生出稳定的设备指纹 —— 同一账号每次请求保持一致，避免触发风控 */
export interface DeviceIdentity {
  webId: string;
  userId: string;
  teaWebId: string;
}

const deviceCache = new Map<string, DeviceIdentity>();

export function deriveDevice(seed: string): DeviceIdentity {
  const cached = deviceCache.get(seed);
  if (cached) return cached;

  const digest = crypto.createHash("sha256").update(seed, "utf8").digest("hex");
  const numeric = BigInt(`0x${digest.slice(0, 16)}`).toString();
  const identity: DeviceIdentity = {
    webId: `7${numeric.padStart(18, "0").slice(0, 18)}`,
    userId: [
      digest.slice(0, 8),
      digest.slice(8, 12),
      digest.slice(12, 16),
      digest.slice(16, 20),
      digest.slice(20, 32)
    ].join("-"),
    teaWebId: `7${numeric.padStart(18, "1").slice(0, 18)}`
  };
  deviceCache.set(seed, identity);
  return identity;
}

/**
 * 构造发给即梦的 Cookie。
 * 若用户粘贴的是完整 Cookie 则原样透传；只给了 sessionid 时补齐必需字段。
 */
export function buildCookie(credential: string, credentialKind: "sessionid" | "cookie", device: DeviceIdentity): string {
  if (credentialKind === "cookie") {
    const normalized = credential.replace(/^\s*cookie\s*:\s*/i, "").replace(/[\r\n]+/g, "; ");
    // 即便用户给了完整 Cookie，也要保证 sessionid 之类关键字段齐全
    if (/\bsessionid\s*=/i.test(normalized)) return normalized;
    const sessionId = extractSessionId(credential);
    return `${normalized.replace(/;\s*$/, "")}; sessionid=${sessionId}; sessionid_ss=${sessionId}`;
  }

  const sessionId = extractSessionId(credential);
  const expires = new Date(Date.now() + 5184000 * 1000).toUTCString().replace(/GMT$/, "GMT").trim();
  return [
    `_tea_web_id=${device.teaWebId}`,
    "is_staff_user=false",
    "store-region=cn-gd",
    "store-region-src=uid",
    `sid_guard=${sessionId}%7C${Math.floor(Date.now() / 1000)}%7C5184000%7C${encodeURIComponent(expires)}`,
    `uid_tt=${device.userId}`,
    `uid_tt_ss=${device.userId}`,
    `sid_tt=${sessionId}`,
    `sessionid=${sessionId}`,
    `sessionid_ss=${sessionId}`
  ].join("; ");
}

/* ------------------------------------------------------------------ */
/* 代理                                                                */
/* ------------------------------------------------------------------ */

const proxyAgents = new Map<string, ProxyAgent>();

export function getProxyAgent(proxyUrl: string): ProxyAgent {
  const key = proxyUrl.trim();
  const existing = proxyAgents.get(key);
  if (existing) return existing;

  let agent: ProxyAgent;
  try {
    agent = new ProxyAgent({ uri: key, connect: { timeout: 15_000 } });
  } catch (error) {
    throw new JimengProxyError(`代理地址无法解析：${key}（${(error as Error).message}）`);
  }
  proxyAgents.set(key, agent);
  return agent;
}

export function closeAllProxyAgents(): void {
  for (const agent of proxyAgents.values()) void agent.close().catch(() => {});
  proxyAgents.clear();
}

/* ------------------------------------------------------------------ */
/* 请求                                                                */
/* ------------------------------------------------------------------ */

export interface JimengRequestInput {
  /** 账号 id，用于派生稳定的设备指纹 */
  accountId: string;
  uri: string;
  method?: "GET" | "POST";
  credential: string;
  credentialKind: "sessionid" | "cookie";
  params?: Record<string, string | number | boolean | undefined>;
  data?: unknown;
  headers?: Record<string, string>;
  /** 每账号独立代理，形如 http://user:pass@host:port 或 socks5://host:port */
  proxyUrl?: string | null;
  timeoutMs?: number;
  referer?: string;
  /** 网络层重试次数（仅对网络错误生效） */
  retries?: number;
}

export interface JimengResponse<T> {
  data: T;
  status: number;
  /** 用于排查问题 */
  requestId: string | null;
}

const DEFAULT_REFERER = "https://jimeng.jianying.com/ai-tool/image/generate";

export async function jimengRequest<T = unknown>(input: JimengRequestInput): Promise<T> {
  const result = await jimengRequestFull<T>(input);
  return result.data;
}

export async function jimengRequestFull<T = unknown>(
  input: JimengRequestInput
): Promise<JimengResponse<T>> {
  const method = input.method ?? "POST";
  const device = deriveDevice(input.accountId);
  const deviceTime = Math.floor(Date.now() / 1000);
  const sign = buildSign(input.uri, deviceTime);
  const cookie = buildCookie(input.credential, input.credentialKind, device);

  const searchParams = new URLSearchParams({
    aid: config.jimeng.appid,
    device_platform: "web",
    region: "CN",
    webId: device.webId
  });
  for (const [key, value] of Object.entries(input.params ?? {})) {
    if (value === undefined) continue;
    searchParams.set(key, String(value));
  }

  const url = `${config.jimeng.origin}${input.uri}?${searchParams.toString()}`;
  const headers: Record<string, string> = {
    Accept: "application/json, text/plain, */*",
    "Accept-Language": "zh-CN,zh;q=0.9",
    "Cache-Control": "no-cache",
    Pragma: "no-cache",
    Appid: config.jimeng.appid,
    Appvr: config.jimeng.versionCode,
    Pf: config.jimeng.platformCode,
    Origin: config.jimeng.origin,
    Referer: input.referer ?? DEFAULT_REFERER,
    "User-Agent": config.jimeng.userAgent,
    "Device-Time": String(deviceTime),
    Sign: sign,
    "Sign-Ver": "1",
    Cookie: cookie,
    ...(input.headers ?? {})
  };

  const body =
    method === "GET" || input.data === undefined ? undefined : JSON.stringify(input.data);
  if (body !== undefined) headers["Content-Type"] = "application/json";

  const dispatcher = input.proxyUrl?.trim() ? getProxyAgent(input.proxyUrl) : undefined;
  const retries = input.retries ?? 2;
  let lastError: Error | null = null;

  for (let attempt = 0; attempt <= retries; attempt += 1) {
    try {
      const response = await undiciRequest(url, {
        method,
        headers,
        body,
        dispatcher,
        headersTimeout: input.timeoutMs ?? config.requestTimeoutMs,
        bodyTimeout: input.timeoutMs ?? config.requestTimeoutMs
      });

      const text = await response.body.text();
      let payload: unknown;
      try {
        payload = text ? JSON.parse(text) : null;
      } catch {
        throw new JimengError(
          `即梦返回了非 JSON 响应（HTTP ${response.statusCode}）：${text.slice(0, 200)}`,
          { status: response.statusCode }
        );
      }

      if (response.statusCode === 401 || response.statusCode === 403) {
        throw new JimengAuthError(`即梦拒绝了请求（HTTP ${response.statusCode}），登录态可能已失效`);
      }
      if (response.statusCode >= 500) {
        throw new JimengNetworkError(`即梦服务端错误（HTTP ${response.statusCode}）`);
      }

      const envelope = payload as JimengEnvelope<T> | null;
      if (envelope && typeof envelope === "object" && "ret" in envelope) {
        const ret = String(envelope.ret ?? "");
        const errmsg = String(envelope.errmsg ?? "");
        if (ret !== "0") {
          const classified = classifyRet(ret, errmsg);
          if (classified) throw classified;
        }
        return {
          data: envelope.data as T,
          status: response.statusCode,
          requestId: (envelope as { log_id?: string }).log_id ?? null
        };
      }

      return { data: payload as T, status: response.statusCode, requestId: null };
    } catch (error) {
      lastError = error as Error;

      // 业务错误（登录失效 / 积分不足 / 接口报错）不重试，直接抛给上层
      if (error instanceof JimengError && !error.retryable) throw error;

      if (attempt < retries) {
        await new Promise((resolve) => setTimeout(resolve, 600 * (attempt + 1)));
        continue;
      }

      if (error instanceof JimengError) throw error;

      const message = (error as Error).message ?? "未知错误";
      if (input.proxyUrl?.trim()) {
        throw new JimengProxyError(`通过代理 ${input.proxyUrl} 请求即梦失败：${message}`);
      }
      throw new JimengNetworkError(`请求即梦失败：${message}`);
    }
  }

  throw new JimengNetworkError(`请求即梦失败：${(lastError as Error | null)?.message ?? "未知错误"}`);
}
