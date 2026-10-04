import type {
  APIKey,
  Account,
  AccountEvent,
  BulkImportResult,
  GenerationRecord,
  GenerationStats,
  ModelCatalog,
  Overview,
  SelectionStrategy
} from "./types.ts";

const TOKEN = new URLSearchParams(location.search).get("token") ?? "";
if (TOKEN) sessionStorage.setItem("manager.token", TOKEN);
const ACCESS_TOKEN = TOKEN || sessionStorage.getItem("manager.token") || "";

export class ApiError extends Error {
  readonly code: string;
  readonly status: number;
  constructor(message: string, code: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.code = code;
    this.status = status;
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = { ...((init.headers as Record<string, string>) ?? {}) };
  if (init.body) headers["Content-Type"] = "application/json";
  if (ACCESS_TOKEN) headers["X-Access-Token"] = ACCESS_TOKEN;

  let response: Response;
  try {
    response = await fetch(`/api${path}`, { ...init, headers });
  } catch (error) {
    throw new ApiError(`无法连接后端服务：${(error as Error).message}`, "NETWORK", 0);
  }

  const text = await response.text();
  let payload: { ok?: boolean; data?: T; error?: { code: string; message: string } } | null = null;
  try {
    payload = text ? JSON.parse(text) : null;
  } catch {
    throw new ApiError(`服务端返回了非 JSON 响应（HTTP ${response.status}）`, "BAD_RESPONSE", response.status);
  }

  if (!response.ok || !payload?.ok) {
    throw new ApiError(
      payload?.error?.message ?? `请求失败（HTTP ${response.status}）`,
      payload?.error?.code ?? "UNKNOWN",
      response.status
    );
  }
  return payload.data as T;
}

const json = (body: unknown): RequestInit => ({ method: "POST", body: JSON.stringify(body) });

export const api = {
  overview: () => request<Overview>("/pool/overview"),
  probeStatus: () => request<Overview["probe"]>("/pool/probe-status"),

  listAccounts: (params: { keyword?: string; health?: string; enabled?: string } = {}) => {
    const search = new URLSearchParams();
    for (const [key, value] of Object.entries(params)) if (value) search.set(key, value);
    const query = search.toString();
    return request<Account[]>(`/accounts${query ? `?${query}` : ""}`);
  },

  createAccount: (body: Record<string, unknown>) => request<Account>("/accounts", json(body)),
  updateAccount: (id: string, body: Record<string, unknown>) =>
    request<Account>(`/accounts/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
  deleteAccount: (id: string) => request<{ deleted: boolean }>(`/accounts/${id}`, { method: "DELETE" }),

  bulkImport: (text: string) => request<BulkImportResult>("/accounts/bulk", json({ text })),

  setEnabled: (id: string, enabled: boolean) =>
    request<Account>(`/accounts/${id}/enabled`, json({ enabled })),
  activate: (id: string) => request<Account>(`/accounts/${id}/activate`, json({})),
  probe: (id: string) => request<Account>(`/accounts/${id}/probe`, json({})),
  receiveCredit: (id: string) =>
    request<{ received: number | null; totalCredit: number | null; account: Account }>(
      `/accounts/${id}/receive-credit`,
      json({})
    ),
  resetHealth: (id: string) => request<Account>(`/accounts/${id}/reset-health`, json({})),
  events: (id: string, limit = 50) => request<AccountEvent[]>(`/accounts/${id}/events?limit=${limit}`),

  probeAll: (ids?: string[]) => request<Overview["probe"]>("/pool/probe-all", json({ ids })),
  setStrategy: (strategy: SelectionStrategy) =>
    request<{ strategy: SelectionStrategy }>("/pool/strategy", {
      method: "PUT",
      body: JSON.stringify({ strategy })
    }),
  globalEvents: (limit = 80) => request<AccountEvent[]>(`/pool/events?limit=${limit}`),
  exportAccounts: () => request<{ accounts: unknown[] }>("/accounts/export?credentials=1"),

  // —— 聚合网关密钥 ——
  listKeys: () => request<APIKey[]>("/keys"),
  createKey: (body: {
    name: string;
    remark?: string;
    ratePerMinute?: number;
    maxConcurrent?: number;
    dailyQuota?: number;
  }) => request<{ key: APIKey; secret: string }>("/keys", json(body)),
  setKeyEnabled: (id: string, enabled: boolean) =>
    request<APIKey>(`/keys/${id}`, { method: "PATCH", body: JSON.stringify({ enabled }) }),
  deleteKey: (id: string) => request<{ deleted: boolean }>(`/keys/${id}`, { method: "DELETE" }),

  // —— 在管理器里登录 ——
  startLogin: (id: string) =>
    request<{ started: boolean; accountId: string }>(`/accounts/${id}/login`, json({})),
  loginStatus: (id: string) =>
    request<{ loggedIn: boolean; nickname?: string; storeCountry?: string; storeIdc?: string }>(
      `/accounts/${id}/login`
    ),
  captureLogin: (id: string) =>
    request<Account>(`/accounts/${id}/login/capture`, json({})),
  cancelLogin: (id: string) =>
    request<{ closed: boolean }>(`/accounts/${id}/login`, { method: "DELETE" }),

  models: (platform?: string) =>
    request<ModelCatalog>(`/models${platform ? `?platform=${platform}` : ""}`),
  createGeneration: (body: Record<string, unknown>) =>
    request<GenerationRecord>("/generate", json(body)),
  generations: (limit = 50) => request<GenerationRecord[]>(`/generations?limit=${limit}`),
  generation: (id: string) => request<GenerationRecord>(`/generations/${id}`),
  cancelGeneration: (id: string) =>
    request<GenerationRecord>(`/generations/${id}/cancel`, json({})),
  deleteGeneration: (id: string) =>
    request<{ deleted: boolean }>(`/generations/${id}`, { method: "DELETE" }),
  generationStats: () => request<GenerationStats>("/generations/stats")
};
