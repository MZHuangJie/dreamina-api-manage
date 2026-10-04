import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { AccountCard } from "./components/AccountCard.tsx";
import { AccountDetail } from "./components/AccountDetail.tsx";
import {
  AddAccountDialog,
  BulkImportDialog,
  ConfirmDialog,
  EditAccountDialog
} from "./components/Dialogs.tsx";
import { GatewayPanel } from "./components/GatewayPanel.tsx";
import { LoginDialog } from "./components/LoginDialog.tsx";
import { GeneratePanel } from "./components/GeneratePanel.tsx";
import { Empty, Spinner } from "./components/ui.tsx";
import { api } from "./api.ts";
import type { Account, AccountEvent, HealthStatus, Overview, SelectionStrategy } from "./types.ts";

type Toast = { id: number; tone: "success" | "error" | "info"; text: string };

type ViewName = "accounts" | "generate" | "gateway";

/** 从 URL hash 解析当前视图，未知值一律回到账号页。 */
function viewFromHash(): ViewName {
  const raw = location.hash.replace(/^#/, "");
  return raw === "generate" || raw === "gateway" ? raw : "accounts";
}

type DialogState =
  | null
  | { kind: "add" }
  | { kind: "bulk" }
  | { kind: "edit"; account: Account }
  | { kind: "delete"; account: Account }
  | { kind: "login"; account: Account };

const STRATEGY_LABEL: Record<SelectionStrategy, string> = {
  least_failures: "失败最少优先",
  active: "只用当前账号",
  round_robin: "轮询",
  most_credit: "积分最多优先"
};

export function App() {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [overview, setOverview] = useState<Overview | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [events, setEvents] = useState<AccountEvent[]>([]);
  const [keyword, setKeyword] = useState("");
  const [healthFilter, setHealthFilter] = useState<HealthStatus | "">("");
  const [busyIds, setBusyIds] = useState<Set<string>>(new Set());
  const [globalBusy, setGlobalBusy] = useState(false);
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [dialog, setDialog] = useState<DialogState>(null);
  const [view, setView] = useState<ViewName>(viewFromHash());
  const [loadError, setLoadError] = useState<string | null>(null);
  const [loaded, setLoaded] = useState(false);

  const toast = useCallback((text: string, tone: Toast["tone"] = "success") => {
    const id = Date.now() + Math.random();
    setToasts((current) => [...current, { id, tone, text }]);
    setTimeout(() => setToasts((current) => current.filter((t) => t.id !== id)), 4600);
  }, []);

  /* ------------------------------ 数据加载 ------------------------------ */

  const refresh = useCallback(async () => {
    try {
      const [list, ov] = await Promise.all([
        api.listAccounts({ keyword: keyword.trim() || undefined, health: healthFilter || undefined }),
        api.overview()
      ]);
      setAccounts(list);
      setOverview(ov);
      setLoadError(null);
    } catch (error) {
      setLoadError((error as Error).message);
    } finally {
      setLoaded(true);
    }
  }, [keyword, healthFilter]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // 视图与 hash 双向同步，方便直接分享 / 刷新回到同一页面
  useEffect(() => {
    if (location.hash !== `#${view}`) location.hash = view;
  }, [view]);

  useEffect(() => {
    const onHashChange = () => setView(viewFromHash());
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  // 全量探活进行中时高频轮询，结束后停止
  const probing = overview?.probe.running ?? false;
  useEffect(() => {
    if (!probing) return;
    const timer = setInterval(() => void refresh(), 1200);
    return () => clearInterval(timer);
  }, [probing, refresh]);

  // 详情面板的操作记录
  useEffect(() => {
    if (!selectedId) {
      setEvents([]);
      return;
    }
    let cancelled = false;
    api
      .events(selectedId, 50)
      .then((list) => !cancelled && setEvents(list))
      .catch(() => !cancelled && setEvents([]));
    return () => {
      cancelled = true;
    };
  }, [selectedId, accounts]);

  const selected = useMemo(
    () => accounts.find((account) => account.id === selectedId) ?? accounts[0] ?? null,
    [accounts, selectedId]
  );

  useEffect(() => {
    if (!selectedId && accounts.length) setSelectedId(accounts[0]!.id);
    if (selectedId && accounts.length && !accounts.some((a) => a.id === selectedId)) {
      setSelectedId(accounts[0]!.id);
    }
  }, [accounts, selectedId]);

  /* ------------------------------ 通用执行器 ------------------------------ */

  const run = useCallback(
    async <T,>(
      id: string | null,
      action: () => Promise<T>,
      successMessage?: string,
      after?: (result: T) => void
    ): Promise<T | undefined> => {
      if (id) setBusyIds((current) => new Set(current).add(id));
      else setGlobalBusy(true);
      try {
        const result = await action();
        after?.(result);
        if (successMessage) toast(successMessage, "success");
        await refresh();
        return result;
      } catch (error) {
        toast((error as Error).message, "error");
        await refresh();
        return undefined;
      } finally {
        if (id) {
          setBusyIds((current) => {
            const next = new Set(current);
            next.delete(id);
            return next;
          });
        } else {
          setGlobalBusy(false);
        }
      }
    },
    [refresh, toast]
  );

  const busyFor = (id: string) => busyIds.has(id) || globalBusy;

  /* ------------------------------ 统计 ------------------------------ */

  const stats = useMemo(() => {
    const ov = overview;
    return [
      { label: "账号总数", value: ov?.total ?? 0, tone: "" },
      { label: "启用中", value: ov?.enabled ?? 0, tone: "" },
      { label: "登录正常", value: ov?.healthy ?? 0, tone: "green" },
      { label: "登录失效", value: ov?.expired ?? 0, tone: (ov?.expired ?? 0) > 0 ? "red" : "" },
      { label: "异常", value: ov?.errorCount ?? 0, tone: (ov?.errorCount ?? 0) > 0 ? "amber" : "" },
      { label: "可用积分合计", value: ov?.totalCredit ?? 0, tone: "accent" },
      { label: "独立代理", value: ov?.withProxy ?? 0, tone: "" },
      { label: "VIP 账号", value: ov?.vipCount ?? 0, tone: "" }
    ];
  }, [overview]);

  /* ------------------------------ 渲染 ------------------------------ */

  if (!loaded) {
    return (
      <div className="app">
        <div className="empty" style={{ margin: "auto" }}>
          <Spinner />
          <div style={{ marginTop: 10 }}>正在连接后端服务…</div>
        </div>
      </div>
    );
  }

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          <div className="brand-mark">即</div>
          <div>
            <div className="brand-title">Dreamina 账号管理器</div>
            <div className="brand-sub">
              {overview?.total
                ? `${overview.total} 个账号 · 当前：${overview.activeAccount?.name ?? "未设置"}`
                : "多账号统一管理"}
            </div>
          </div>
        </div>

        <div className="tabs" style={{ marginBottom: 0 }}>
          <button
            className={`tab${view === "accounts" ? " active" : ""}`}
            onClick={() => setView("accounts")}
          >
            账号
          </button>
          <button
            className={`tab${view === "generate" ? " active" : ""}`}
            onClick={() => setView("generate")}
          >
            生成
          </button>
          <button
            className={`tab${view === "gateway" ? " active" : ""}`}
            onClick={() => setView("gateway")}
          >
            接入
          </button>
        </div>

        <select
          className="select"
          style={{ width: 158 }}
          value={overview?.strategy ?? "least_failures"}
          onChange={(event) =>
            void run(null, () => api.setStrategy(event.target.value as SelectionStrategy), "调度策略已更新")
          }
          title="自动调度时如何挑选账号"
        >
          {(Object.keys(STRATEGY_LABEL) as SelectionStrategy[]).map((key) => (
            <option key={key} value={key}>
              {STRATEGY_LABEL[key]}
            </option>
          ))}
        </select>

        {view === "accounts" ? (
          <>
            <button
              className="btn"
              disabled={globalBusy || probing || !accounts.length}
              onClick={() => void run(null, () => api.probeAll(), undefined)}
              title="检测所有启用账号的登录态并刷新积分"
            >
              {probing ? <Spinner /> : null} 全量探活
            </button>
            <button className="btn" onClick={() => setDialog({ kind: "bulk" })}>
              批量导入
            </button>
            <button className="btn btn-primary" onClick={() => setDialog({ kind: "add" })}>
              + 添加账号
            </button>
          </>
        ) : null}
      </header>

      {view === "accounts" ? (
        <div className="stats">
          {stats.map((stat) => (
            <div className="stat" key={stat.label}>
              <div className="stat-label">{stat.label}</div>
              <div className={`stat-value ${stat.tone}`}>{stat.value}</div>
            </div>
          ))}
        </div>
      ) : null}

      {probing && overview ? (
        <div style={{ padding: "0 22px" }}>
          <div className="progress">
            <div
              className="progress-bar"
              style={{
                width: `${overview.probe.total ? (overview.probe.completed / overview.probe.total) * 100 : 0}%`
              }}
            />
          </div>
          <div className="field-hint" style={{ marginTop: 5 }}>
            正在探活 {overview.probe.completed}/{overview.probe.total} · 正常 {overview.probe.healthy} · 异常{" "}
            {overview.probe.failed}
          </div>
        </div>
      ) : null}

      {loadError ? (
        <div style={{ padding: "10px 22px 0" }}>
          <div className="alert alert-error" style={{ marginBottom: 0 }}>
            {loadError}
          </div>
        </div>
      ) : null}

      {view === "generate" ? (
        <div className="body">
          <div className="generate-wrap">
            <div className="generate-inner">
              <GeneratePanel accounts={accounts} onToast={toast} />
            </div>
          </div>
        </div>
      ) : view === "gateway" ? (
        <div className="body">
          <div className="generate-wrap">
            <div className="generate-inner">
              <GatewayPanel onToast={toast} />
            </div>
          </div>
        </div>
      ) : (
      <div className="body">
        <aside className="sidebar">
          <div className="row">
            <input
              className="input"
              placeholder="搜索名称 / 昵称 / uid"
              value={keyword}
              onChange={(event) => setKeyword(event.target.value)}
            />
            <select
              className="select"
              style={{ width: 116 }}
              value={healthFilter}
              onChange={(event) => setHealthFilter(event.target.value as HealthStatus | "")}
            >
              <option value="">全部状态</option>
              <option value="healthy">正常</option>
              <option value="expired">登录失效</option>
              <option value="error">异常</option>
              <option value="unknown">未检测</option>
            </select>
          </div>

          <div className="list">
            {accounts.length === 0 ? (
              <Empty
                icon="＋"
                title={overview?.total ? "没有匹配的账号" : "还没有添加账号"}
                hint={
                  overview?.total
                    ? "换个关键词或状态筛选试试"
                    : "点击右上角「添加账号」，或使用「批量导入」粘贴多个 sessionid"
                }
              />
            ) : (
              accounts.map((account) => (
                <AccountCard
                  key={account.id}
                  account={account}
                  selected={selected?.id === account.id}
                  busy={busyIds.has(account.id)}
                  onSelect={() => setSelectedId(account.id)}
                />
              ))
            )}
          </div>
        </aside>

        <main className="detail">
          {selected ? (
            <AccountDetail
              account={selected}
              events={events}
              busy={busyFor(selected.id)}
              onActivate={() =>
                void run(selected.id, () => api.activate(selected.id), `已切换到「${selected.name}」`)
              }
              onRelogin={() => setDialog({ kind: "login", account: selected })}
              onProbe={() =>
                void run(
                  selected.id,
                  () => api.probe(selected.id),
                  undefined,
                  (account) =>
                    toast(
                      account.health === "healthy"
                        ? `「${account.name}」登录正常，积分 ${account.totalCredit ?? "未知"}`
                        : `「${account.name}」${account.statusError ?? "检测异常"}`,
                      account.health === "healthy" ? "success" : "error"
                    )
                )
              }
              onReceiveCredit={() =>
                void run(
                  selected.id,
                  () => api.receiveCredit(selected.id),
                  undefined,
                  (result) =>
                    toast(
                      result.received
                        ? `领取成功，获得 ${result.received} 积分，当前 ${result.totalCredit ?? "?"}`
                        : `今日无可领取积分，当前 ${result.totalCredit ?? "?"}`,
                      result.received ? "success" : "info"
                    )
                )
              }
              onToggleEnabled={() =>
                void run(
                  selected.id,
                  () => api.setEnabled(selected.id, !selected.enabled),
                  selected.enabled ? "账号已停用" : "账号已启用"
                )
              }
              onResetHealth={() =>
                void run(selected.id, () => api.resetHealth(selected.id), "状态已重置，将重新参与调度")
              }
              onEdit={() => setDialog({ kind: "edit", account: selected })}
              onDelete={() => setDialog({ kind: "delete", account: selected })}
            />
          ) : (
            <Empty title="从左侧选择一个账号" hint="或者先添加一个 Dreamina 账号" icon="即" />
          )}
        </main>
      </div>
      )}

      {dialog?.kind === "add" ? (
        <AddAccountDialog
          onClose={() => setDialog(null)}
          onSubmit={async (payload) => {
            const created = await run(null, () => api.createAccount(payload), undefined);
            if (created) {
              setSelectedId(created.id);
              toast(
                created.health === "healthy"
                  ? `「${created.name}」添加成功，积分 ${created.totalCredit ?? "未知"}`
                  : `「${created.name}」已添加，但检测未通过：${created.statusError ?? "未知原因"}`,
                created.health === "healthy" ? "success" : "error"
              );
            }
          }}
        />
      ) : null}

      {dialog?.kind === "bulk" ? (
        <BulkImportDialog
          onClose={() => setDialog(null)}
          onSubmit={async (text) => {
            const result = await run(null, () => api.bulkImport(text));
            return result ?? { created: 0, failed: 0, errors: [] };
          }}
        />
      ) : null}

      {dialog?.kind === "edit" ? (
        <EditAccountDialog
          account={dialog.account}
          onClose={() => setDialog(null)}
          onSubmit={async (payload) => {
            await run(dialog.account.id, () => api.updateAccount(dialog.account.id, payload), "账号已更新");
          }}
        />
      ) : null}

      {dialog?.kind === "login" ? (
        <LoginDialog
          account={dialog.account}
          onClose={() => setDialog(null)}
          onDone={() => {
            setDialog(null);
            void refresh();
          }}
          onToast={toast}
        />
      ) : null}

      {dialog?.kind === "delete" ? (
        <ConfirmDialog
          title="删除账号"
          danger
          confirmLabel="确认删除"
          message={
            <>
              确定要删除「<strong>{dialog.account.name}</strong>」吗？
              <div style={{ color: "var(--muted)", marginTop: 7, fontSize: 12.5 }}>
                该账号的凭据与状态数据会一并从本地数据库移除，此操作不可撤销。
              </div>
            </>
          }
          onClose={() => setDialog(null)}
          onConfirm={() => void run(dialog.account.id, () => api.deleteAccount(dialog.account.id), "账号已删除")}
        />
      ) : null}

      <div className="toasts">
        {toasts.map((item) => (
          <div key={item.id} className={`toast toast-${item.tone}`}>
            {item.text}
          </div>
        ))}
      </div>
    </div>
  );
}
