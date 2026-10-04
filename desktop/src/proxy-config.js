// 会话代理配置。
//
// 单独成一个模块的原因：这段逻辑要在「业务会话」和「登录窗口」两处用，
// 内联写两遍容易改漏一处——之前强制直连那个 bug 就是这么来的。

/**
 * 必须绕开代理的域名。
 *
 * 有些域名走系统代理会连接被掐断（ERR_CONNECTION_CLOSED），直连反而正常。
 * 最要命的是 mon.capcutapi.us —— 字节的风控 SDK（webmssdk）挂在它上面，
 * 它加载不了，登录表单提交就会静默失败，表现是「输入邮箱点继续没反应」。
 *
 * 这些域名在国内可以直连，走代理反而坏，所以排除掉。
 */
const BYPASS_HOSTS = [
  "mon.capcutapi.us",
  "*.mon.capcutapi.us",
  "bat.bing.com",
  "www.googletagmanager.com",
  "www.google-analytics.com"
];

function normalize(raw) {
  const value = String(raw ?? "").trim();
  if (!value) return "";
  return /^[a-z0-9+.-]+:\/\//i.test(value) ? value : `http://${value}`;
}

/**
 * 给一个 Electron 会话设置代理。
 *
 * 关键是那只绕过清单。Electron 的 proxyBypassRules 只在 fixed_servers 模式下
 * 生效，mode 为 system 时会被忽略；所以这里先用 resolveProxy 从系统设置里
 * 问出实际代理地址，再用显式规则配上去，这样绕过清单才带得上。
 */
async function applySessionProxy(ses, accountProxy, label, log) {
  const say = typeof log === "function" ? log : () => {};
  const explicit = normalize(accountProxy);

  if (explicit) {
    await ses.setProxy({
      proxyRules: explicit,
      proxyBypassRules: BYPASS_HOSTS.join(",")
    });
    say(`${label} 走账号代理 ${explicit}，已绕过 ${BYPASS_HOSTS.length} 个域名`);
    return;
  }

  if (process.env.MANAGER_IGNORE_SYSTEM_PROXY === "true") {
    await ses.setProxy({ mode: "direct" });
    say(`${label} 强制直连（MANAGER_IGNORE_SYSTEM_PROXY=true）`);
    return;
  }

  let resolved = "";
  try {
    resolved = await ses.resolveProxy("https://dreamina.capcut.com");
  } catch {
    /* 解析失败就退回 system 模式，至少不比原来差 */
  }

  const m = /PROXY\s+([^;\s]+)/i.exec(resolved || "");
  if (!m) {
    // 没解析出来（没开代理，或用了 PAC）。退回 system，只是带不上绕过清单。
    await ses.setProxy({ mode: "system" });
    say(`${label} 跟随系统代理（未解析出地址，绕过清单未生效）`);
    return;
  }

  const rules = normalize(m[1]);
  await ses.setProxy({
    proxyRules: rules,
    proxyBypassRules: BYPASS_HOSTS.join(",")
  });
  say(`${label} 走系统代理 ${rules}，已绕过 ${BYPASS_HOSTS.length} 个域名`);
}

module.exports = { applySessionProxy, BYPASS_HOSTS };
