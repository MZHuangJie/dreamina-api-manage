import { useEffect, useRef, useState } from "react";

import { api } from "../api.ts";
import type { Account } from "../types.ts";
import { Modal, Spinner } from "./ui.tsx";

/**
 * 在管理器里登录。
 *
 * 流程：管理器弹出一个真实浏览器窗口 → 用户在里面正常登录 →
 * 本组件轮询登录态 → 检测到就自动保存凭据并关闭窗口。
 *
 * 之所以这么做而不是让用户去抠 cookie：账密、邮箱验证码、Google 登录
 * 这些方式都能用，而且不会有「复制错了」这类问题。
 */
export function LoginDialog({
  account,
  onClose,
  onDone,
  onToast
}: {
  account: Account;
  onClose: () => void;
  onDone: (updated: Account) => void;
  onToast: (text: string, tone?: "success" | "error" | "info") => void;
}) {
  const [phase, setPhase] = useState<"starting" | "waiting" | "saving" | "failed">("starting");
  const [error, setError] = useState<string | null>(null);
  const [elapsed, setElapsed] = useState(0);
  const stopped = useRef(false);
  // StrictMode 在开发模式下会把 effect 跑两次。ref 在两次之间是保留的，
  // 用它守住「开窗口」这个动作——否则会连开两个浏览器窗口，
  // 而且用户很可能在没被跟踪的那个里登录，导致怎么登都没反应。
  const startedRef = useRef(false);
  // 回调放进 ref，避免它们每次渲染生成新函数、把 effect 反复重启
  const onDoneRef = useRef(onDone);
  const onToastRef = useRef(onToast);
  onDoneRef.current = onDone;
  onToastRef.current = onToast;

  useEffect(() => {
    stopped.current = false;
    let timer: ReturnType<typeof setInterval> | null = null;

    (async () => {
      try {
        if (!startedRef.current) {
          startedRef.current = true;
          await api.startLogin(account.id);
        }
        setPhase("waiting");
      } catch (err) {
        setPhase("failed");
        setError((err as Error).message);
        return;
      }

      // 登录可能要花几分钟（收验证码、选账号），所以轮询得很耐心
      const startedAt = Date.now();
      timer = setInterval(async () => {
        if (stopped.current) return;
        setElapsed(Math.floor((Date.now() - startedAt) / 1000));
        try {
          const status = await api.loginStatus(account.id);
          if (!status.loggedIn) return;

          if (timer) clearInterval(timer);
          setPhase("saving");
          const updated = await api.captureLogin(account.id);
          stopped.current = true;
          onToastRef.current(`「${updated.name}」已重新登录，凭据已更新`, "success");
          onDoneRef.current(updated);
        } catch {
          // 轮询期间的瞬时错误（窗口还没建好等）直接忽略，下一轮再试
        }
      }, 2500);
    })();

    return () => {
      stopped.current = true;
      if (timer) clearInterval(timer);
    };
  }, [account.id]);

  async function cancel() {
    stopped.current = true;
    try {
      await api.cancelLogin(account.id);
    } catch {
      /* 窗口可能已经关了 */
    }
    onClose();
  }

  const minutes = Math.floor(elapsed / 60);
  const seconds = elapsed % 60;

  return (
    <Modal
      title="在管理器里登录"
      onClose={() => void cancel()}
      footer={
        <button className="btn" onClick={() => void cancel()}>
          取消
        </button>
      }
    >
      {phase === "failed" ? (
        <div className="alert alert-error">
          <div style={{ marginBottom: 6 }}>打开登录窗口失败</div>
          <div style={{ fontSize: 12 }}>{error}</div>
          <div style={{ fontSize: 12, marginTop: 8, color: "var(--text-dim)" }}>
            请确认浏览器通道已启动（桌面端会自动启动；命令行方式见使用说明）。
          </div>
        </div>
      ) : (
        <>
          <div className="alert alert-info">
            已经为你打开了一个浏览器窗口，请在其中完成登录。
            <br />
            <strong>账密、邮箱验证码、Google 登录都可以</strong>——就是正常登录一次。
          </div>

          <div style={{ display: "flex", alignItems: "center", gap: 10, padding: "10px 0" }}>
            {phase === "saving" ? (
              <>
                <Spinner />
                <span>检测到登录成功，正在保存凭据并刷新账号…</span>
              </>
            ) : (
              <>
                <Spinner />
                <span>
                  等待登录完成… 已等待 {minutes} 分 {seconds} 秒
                </span>
              </>
            )}
          </div>

          <div className="field-hint">
            登录成功后凭据会<strong>自动保存</strong>，然后我们会立刻探活一次刷新积分。
            不用手动复制 cookie。
          </div>
        </>
      )}
    </Modal>
  );
}
