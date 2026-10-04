import type { Context } from "hono";
import { z } from "zod";

import { AccountError } from "./accounts/pool.ts";
import { JimengAuthError, JimengCreditError, JimengError, JimengProxyError } from "./jimeng/errors.ts";

export function ok<T>(c: Context, data: T, status = 200): Response {
  return c.json({ ok: true, data }, status as 200);
}

export function fail(c: Context, error: unknown): Response {
  let status = 500;
  let code = "INTERNAL";
  let message = "服务器内部错误";

  if (error instanceof AccountError) {
    status = error.status;
    code = "ACCOUNT";
    message = error.message;
  } else if (error instanceof JimengAuthError) {
    status = 401;
    code = error.code ?? "AUTH_EXPIRED";
    message = error.message;
  } else if (error instanceof JimengCreditError) {
    status = 402;
    code = "INSUFFICIENT_CREDIT";
    message = error.message;
  } else if (error instanceof JimengProxyError) {
    status = 502;
    code = "PROXY";
    message = error.message;
  } else if (error instanceof JimengError) {
    status = 502;
    code = error.code ?? "JIMENG";
    message = error.message;
  } else if (error instanceof z.ZodError) {
    status = 400;
    code = "VALIDATION";
    message = error.issues.map((i) => `${i.path.join(".") || "body"}: ${i.message}`).join("；");
  } else if (error instanceof Error) {
    message = error.message;
  }

  return c.json({ ok: false, error: { code, message } }, status as 400);
}

/** 读取并校验 JSON body */
export async function readJson<T extends z.ZodTypeAny>(
  c: Context,
  schema: T
): Promise<z.infer<T>> {
  let body: unknown;
  try {
    body = await c.req.json();
  } catch {
    throw new AccountError("请求体不是合法 JSON");
  }
  return schema.parse(body) as z.infer<T>;
}
