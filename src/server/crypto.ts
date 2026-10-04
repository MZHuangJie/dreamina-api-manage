import crypto from "node:crypto";
import fs from "node:fs";

import { config, ensureDataDir } from "./config.ts";

const KEY_BYTES = 32;

let cachedKey: Buffer | null = null;

/**
 * 获取账号凭据加密密钥。
 *
 * 优先级：
 *   1. MANAGER_SECRET 环境变量（内容会被 sha256 派生成 32 字节）
 *   2. data/secret.key 密钥文件（首次运行自动生成）
 *
 * 密钥文件丢失后，已入库的凭据将无法解密 —— 这是刻意的设计，
 * 因为数据库里存的是即梦账号的登录态，等同于账号本身。
 */
export function getKey(): Buffer {
  if (cachedKey) return cachedKey;

  const fromEnv = process.env.MANAGER_SECRET?.trim();
  if (fromEnv) {
    cachedKey = crypto.createHash("sha256").update(fromEnv, "utf8").digest();
    return cachedKey;
  }

  ensureDataDir();
  if (fs.existsSync(config.keyPath)) {
    const raw = fs.readFileSync(config.keyPath, "utf8").trim();
    const key = Buffer.from(raw, "base64");
    if (key.length !== KEY_BYTES) {
      throw new Error(
        `密钥文件 ${config.keyPath} 内容无效（期望 ${KEY_BYTES} 字节，实际 ${key.length} 字节）。` +
          "请从备份恢复，或删除该文件后重新添加账号。"
      );
    }
    cachedKey = key;
    return cachedKey;
  }

  const generated = crypto.randomBytes(KEY_BYTES);
  fs.writeFileSync(config.keyPath, generated.toString("base64"), { encoding: "utf8", mode: 0o600 });
  cachedKey = generated;
  return cachedKey;
}

/** AES-256-GCM 加密，输出 v1.<iv>.<tag>.<ciphertext>（base64url） */
export function encryptSecret(plaintext: string): string {
  const iv = crypto.randomBytes(12);
  const cipher = crypto.createCipheriv("aes-256-gcm", getKey(), iv);
  const ciphertext = Buffer.concat([cipher.update(plaintext, "utf8"), cipher.final()]);
  return [
    "v1",
    iv.toString("base64url"),
    cipher.getAuthTag().toString("base64url"),
    ciphertext.toString("base64url")
  ].join(".");
}

export function decryptSecret(payload: string): string {
  const parts = payload.split(".");
  if (parts.length !== 4 || parts[0] !== "v1") {
    throw new Error("凭据密文格式无效");
  }
  const [, ivText, tagText, dataText] = parts as [string, string, string, string];
  const decipher = crypto.createDecipheriv("aes-256-gcm", getKey(), Buffer.from(ivText, "base64url"));
  decipher.setAuthTag(Buffer.from(tagText, "base64url"));
  try {
    return Buffer.concat([
      decipher.update(Buffer.from(dataText, "base64url")),
      decipher.final()
    ]).toString("utf8");
  } catch {
    throw new Error(
      "凭据解密失败：当前密钥与写入时不一致。请确认 data/secret.key（或 MANAGER_SECRET）未被更换。"
    );
  }
}

/** 用于去重与展示，不可逆 */
export function hashSecret(value: string): string {
  return crypto.createHash("sha256").update(value, "utf8").digest("hex");
}

export function newId(): string {
  return crypto.randomUUID();
}

/**
 * 解析用户粘贴的登录凭据，提取出即梦接口所需的 sessionid。
 * 兼容：纯 sessionid、`sessionid=xxx`、`Cookie: a=b; c=d`、多行粘贴。
 */
export function extractSessionId(raw: string): string {
  const text = raw.trim();
  if (!text) throw new Error("凭据不能为空");

  const normalized = text.replace(/^\s*cookie\s*:\s*/i, "").replace(/[\r\n]+/g, ";");
  const pairs = new Map<string, string>();
  for (const item of normalized.split(";")) {
    const separator = item.indexOf("=");
    if (separator < 0) continue;
    const key = item.slice(0, separator).trim().toLowerCase();
    const value = item.slice(separator + 1).trim();
    if (key && value) pairs.set(key, value);
  }

  let value =
    pairs.get("sessionid") ??
    pairs.get("sessionid_ss") ??
    pairs.get("sid_tt") ??
    pairs.get("sid_guard")?.split("%7C")[0] ??
    pairs.get("sid_guard")?.split("|")[0];

  // 允许直接粘贴裸 sessionid
  if (!value && !normalized.includes("=") && /^[^\s;]+$/.test(text)) value = text;

  if (!value) {
    throw new Error("未能从凭据中识别 sessionid，请确认复制的是即梦官网登录后的 Cookie 或 sessionid");
  }
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

/** 脱敏预览 */
export function previewSecret(sessionId: string): string {
  if (sessionId.length <= 10) return "********";
  return `${sessionId.slice(0, 4)}…${sessionId.slice(-4)}`;
}
