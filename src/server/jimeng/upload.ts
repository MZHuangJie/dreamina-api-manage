import crypto from "node:crypto";
import path from "node:path";

import { request as undiciRequest } from "undici";

import { config } from "../config.ts";
import { getProxyAgent, jimengRequest } from "./client.ts";
import { JimengError } from "./errors.ts";

/**
 * 参考图 / 首帧图上传。
 * 即梦的图片资源托管在字节 ImageX 上，流程是：
 *   1. 向即梦要一个上传凭证（STS 临时密钥）
 *   2. 用 AWS SigV4 调 ImageX ApplyImageUpload 拿到上传地址
 *   3. 把二进制 PUT 上去（带 CRC32 校验头）
 *   4. 调 CommitImageUpload 提交，拿到最终 image_uri
 */

const IMAGEX_HOST = "https://imagex.bytedanceapi.com";
const SERVICE_ID = "tb4s082cfz";
const REGION = "cn-north-1";
const SERVICE = "imagex";
const API_VERSION = "2018-08-01";

/** CRC32（IEEE），用于 ImageX 的 Content-Crc32 校验头 */
const CRC_TABLE = (() => {
  const table = new Uint32Array(256);
  for (let i = 0; i < 256; i += 1) {
    let c = i;
    for (let k = 0; k < 8; k += 1) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    table[i] = c >>> 0;
  }
  return table;
})();

export function crc32(buffer: Uint8Array): number {
  let crc = 0xffffffff;
  for (let i = 0; i < buffer.length; i += 1) {
    crc = (CRC_TABLE[(crc ^ buffer[i]!) & 0xff]! ^ (crc >>> 8)) >>> 0;
  }
  return (crc ^ 0xffffffff) >>> 0;
}

function randomString(length = 11): string {
  const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789";
  const bytes = crypto.randomBytes(length);
  let out = "";
  for (let i = 0; i < length; i += 1) out += alphabet[bytes[i]! % alphabet.length];
  return out;
}

interface UploadAuth {
  access_key_id: string;
  secret_access_key: string;
  session_token: string;
}

function buildAwsHeaders(
  auth: UploadAuth,
  method: string,
  params: Record<string, string>,
  body: Record<string, unknown> = {}
): Record<string, string> {
  const now = new Date();
  const amzDate = now.toISOString().replace(/[:\-]|\.\d{3}/g, "").slice(0, 15) + "Z";
  const amzDay = amzDate.slice(0, 8);

  const headers: Record<string, string> = {
    "X-Amz-Date": amzDate,
    "X-Amz-Security-Token": auth.session_token
  };
  if (Object.keys(body).length > 0) {
    headers["X-Amz-Content-Sha256"] = crypto
      .createHash("sha256")
      .update(JSON.stringify(body))
      .digest("hex");
  }

  const credentialScope = `${amzDay}/${REGION}/${SERVICE}/aws4_request`;
  const sortedKeys = Object.keys(headers).sort();
  const signedHeaders = sortedKeys.map((k) => k.toLowerCase()).join(";");
  const canonicalHeaders =
    sortedKeys.map((k) => `${k.toLowerCase()}:${headers[k]}`).join("\n") + "\n";

  const bodyHash =
    Object.keys(body).length > 0
      ? crypto.createHash("sha256").update(JSON.stringify(body)).digest("hex")
      : crypto.createHash("sha256").update("").digest("hex");

  const canonicalRequest = [
    method.toUpperCase(),
    "/",
    new URLSearchParams(params).toString(),
    canonicalHeaders,
    signedHeaders,
    bodyHash
  ].join("\n");

  const stringToSign = [
    "AWS4-HMAC-SHA256",
    amzDate,
    credentialScope,
    crypto.createHash("sha256").update(canonicalRequest).digest("hex")
  ].join("\n");

  const kDate = crypto.createHmac("sha256", `AWS4${auth.secret_access_key}`).update(amzDay).digest();
  const kRegion = crypto.createHmac("sha256", kDate).update(REGION).digest();
  const kService = crypto.createHmac("sha256", kRegion).update(SERVICE).digest();
  const signingKey = crypto.createHmac("sha256", kService).update("aws4_request").digest();
  const signature = crypto.createHmac("sha256", signingKey).update(stringToSign).digest("hex");

  return {
    ...headers,
    Authorization: `AWS4-HMAC-SHA256 Credential=${auth.access_key_id}/${credentialScope}, SignedHeaders=${signedHeaders}, Signature=${signature}`
  };
}

/** ImageX 的错误响应键历史上带过一个尾随空格，两种都兼容 */
function readImagexError(payload: unknown): string | null {
  if (!payload || typeof payload !== "object") return null;
  const record = payload as Record<string, { Error?: { Message?: string; Code?: string } }>;
  const envelope = record["Response "] ?? record.Response;
  if (envelope?.Error) return envelope.Error.Message ?? envelope.Error.Code ?? "ImageX 返回错误";
  return null;
}

export interface UploadTarget {
  accountId: string;
  credential: string;
  credentialKind: "sessionid" | "cookie";
  proxyUrl?: string | null;
}

export interface UploadableImage {
  data: Buffer;
  filename: string;
  contentType: string;
}

/** 把一张图片上传到即梦，返回 image_uri */
export async function uploadImage(
  target: UploadTarget,
  image: UploadableImage,
  options: { forVideo?: boolean } = {}
): Promise<string> {
  const dispatcher = target.proxyUrl?.trim() ? getProxyAgent(target.proxyUrl) : undefined;

  // 1) 取上传凭证。视频参考图与普通图片的 scene 不同，失败时互相回退。
  const scenes: (string | number)[] = options.forVideo ? ["video_cover", 2] : [2];
  let auth: UploadAuth | null = null;
  let lastError: unknown = null;
  for (const scene of scenes) {
    try {
      const result = await jimengRequest<UploadAuth>({
        accountId: target.accountId,
        uri: "/mweb/v1/get_upload_token",
        credential: target.credential,
        credentialKind: target.credentialKind,
        params: { aid: config.jimeng.appid, da_version: "3.2.2", aigc_features: "app_lip_sync" },
        data: { scene },
        proxyUrl: target.proxyUrl
      });
      if (result?.access_key_id && result.secret_access_key && result.session_token) {
        auth = result;
        break;
      }
    } catch (error) {
      lastError = error;
    }
  }
  if (!auth) {
    throw new JimengError(
      `获取上传凭证失败，账号可能已掉线：${(lastError as Error | null)?.message ?? "即梦未返回有效凭证"}`
    );
  }

  // 2) ApplyImageUpload
  const applyParams = {
    Action: "ApplyImageUpload",
    FileSize: String(image.data.length),
    ServiceId: SERVICE_ID,
    Version: API_VERSION,
    s: randomString(11)
  };
  const applyResponse = await undiciRequest(
    `${IMAGEX_HOST}/?${new URLSearchParams(applyParams).toString()}`,
    { method: "GET", headers: buildAwsHeaders(auth, "GET", applyParams), dispatcher }
  );
  const applyPayload = (await applyResponse.body.json()) as Record<string, unknown>;

  const applyError = readImagexError(applyPayload);
  if (applyError) throw new JimengError(`申请上传地址失败：${applyError}`);

  const applyResult = applyPayload.Result as {
    UploadAddress: {
      UploadHosts: string[];
      StoreInfos: { StoreUri: string; Auth: string }[];
      SessionKey: string;
    };
  };
  const address = applyResult?.UploadAddress;
  const store = address?.StoreInfos?.[0];
  if (!address || !store) throw new JimengError("申请上传地址失败：即梦未返回上传信息");

  // 3) 上传二进制
  const uploadUrl = `https://${address.UploadHosts[0]}/upload/v1/${store.StoreUri}`;
  const uploadResponse = await undiciRequest(uploadUrl, {
    method: "POST",
    headers: {
      Authorization: store.Auth,
      "Content-Crc32": crc32(image.data).toString(16),
      "Content-Type": "application/octet-stream"
    },
    body: image.data,
    dispatcher
  });
  const uploadPayload = (await uploadResponse.body.json()) as { code?: number; message?: string };
  if (uploadPayload.code !== 2000) {
    throw new JimengError(`上传图片失败：${uploadPayload.message ?? `code ${uploadPayload.code}`}`);
  }

  // 4) CommitImageUpload
  const commitParams = {
    Action: "CommitImageUpload",
    FileSize: String(image.data.length),
    ServiceId: SERVICE_ID,
    Version: API_VERSION
  };
  const commitBody = { SessionKey: address.SessionKey };
  const commitResponse = await undiciRequest(
    `${IMAGEX_HOST}/?${new URLSearchParams(commitParams).toString()}`,
    {
      method: "POST",
      headers: { ...buildAwsHeaders(auth, "POST", commitParams, commitBody), "Content-Type": "application/json" },
      body: JSON.stringify(commitBody),
      dispatcher
    }
  );
  const commitPayload = (await commitResponse.body.json()) as Record<string, unknown>;

  const commitError = readImagexError(commitPayload);
  if (commitError) throw new JimengError(`提交图片失败：${commitError}`);

  const commitResult = commitPayload.Result as { Results?: { Uri?: string }[] };
  const uri = commitResult?.Results?.[0]?.Uri;
  if (!uri) throw new JimengError("提交图片失败：即梦未返回图片 URI");
  return uri;
}

const MIME_BY_EXT: Record<string, string> = {
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".webp": "image/webp",
  ".gif": "image/gif",
  ".bmp": "image/bmp"
};

export function guessContentType(filename: string): string {
  return MIME_BY_EXT[path.extname(filename).toLowerCase()] ?? "image/png";
}

/** 支持 dataURL / 网络 URL / 本地路径三种输入 */
export async function loadImageBytes(source: string): Promise<UploadableImage> {
  const value = source.trim();

  if (value.startsWith("data:")) {
    const match = value.match(/^data:([^;]+);base64,(.*)$/s);
    if (!match) throw new JimengError("无法解析 dataURL 图片");
    return {
      data: Buffer.from(match[2]!, "base64"),
      filename: `upload.${(match[1]!.split("/")[1] ?? "png").replace("jpeg", "jpg")}`,
      contentType: match[1]!
    };
  }

  if (/^https?:\/\//i.test(value)) {
    const response = await undiciRequest(value, { method: "GET" });
    if (response.statusCode >= 400) {
      throw new JimengError(`下载参考图失败（HTTP ${response.statusCode}）`);
    }
    const data = Buffer.from(await response.body.arrayBuffer());
    const filename = path.basename(new URL(value).pathname) || "upload.png";
    return { data, filename, contentType: guessContentType(filename) };
  }

  const fs = await import("node:fs/promises");
  const absolute = path.resolve(value);
  const data = await fs.readFile(absolute);
  const filename = path.basename(absolute);
  return { data, filename, contentType: guessContentType(filename) };
}
