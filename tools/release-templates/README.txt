即梦 / Dreamina 账号管理器
==========================

【环境要求】
  Windows 10 及以上（64 位）
  Node.js 20+        —— sidecar 需要；核心本身不需要
  Google Chrome      —— 浏览器通道会调用它

【启动】
  双击「start.bat」，然后浏览器打开 http://127.0.0.1:8787
  停止：双击「stop.bat」

【添加账号】
  Dreamina（海外版）
    1. 在 dreamina.capcut.com 登录（保持代理开启，地区建议与注册地一致）
    2. F12 → Application → Cookies → 选中 .capcut.com 域
    3. 复制整段 Cookie（或至少 sessionid + store-idc）
    4. 粘贴进管理器的「添加账号」，平台选 Dreamina

    管理器会自动从 Cookie 里识别 store-idc，据此选中正确的区域集群。
    这一步很关键：账号属于哪个集群就必须打哪个集群的接口，
    地区搞错会返回 1015 login error，看起来像登录失效，其实只是打错了地方。

  即梦（国内版）
    在 jimeng.jianying.com 登录后同样方式取 sessionid，平台选「即梦」。

【代理】
  每个账号可以单独配代理。Dreamina 的账号归属地在注册时就确定了，
  建议代理出口地区与账号地区保持一致，不要频繁切换。
  注意：这个系统里有两个"代理"概念——
    · 账号代理：账号访问平台时走的出口
    · 本机代理：sidecar 启动浏览器时走的出口
  两者独立。如果账号配了代理，sidecar 会用账号代理；否则用本机网络。

【数据目录 data/】
  manager.db    账号、状态、生成记录、操作日志
  secret.key    凭据加密密钥 —— 务必备份！丢了已保存的账号就解不开了

【配置（环境变量，可在 start.bat 里改）】
  MANAGER_PORT             监听端口，默认 8787
  MANAGER_ACCESS_TOKEN     访问令牌，开放到局域网时必设
  MANAGER_HEALTH_ENABLED   保活巡检开关，默认 true
  MANAGER_HEALTH_INTERVAL_MS  巡检间隔，默认 30 分钟
  SIDECAR_URL              浏览器通道地址，默认 http://127.0.0.1:8790
  SIDECAR_SECRET           浏览器通道共享密钥（可选，本机部署可不设）
  SIDECAR_HEADLESS         浏览器是否无头，默认 true

【架构说明】
  ┌─ manager.exe（Go 单二进制 / 无 cgo）
  │    账号池、调度、存储、保活巡检、生成任务队列、HTTP API
  │    所有**读**操作（积分、列表、轮询结果）由核心直连平台，不经过浏览器
  └─ sidecar/（Node）
       唯一职责：在已登录的页面上下文里代发 HTTP 请求

  为什么需要 sidecar：
    Dreamina 的**写**操作（提交生成）必须携带浏览器端生成的风控签名
    —— msToken / X-Bogus / X-Gnarly，这三个参数由页面的 secsdk 计算并
    附加到 URL 上，纯服务端无法复现（社区也没有可用的 Go 实现）。
    而 Playwright 的驱动本身是 Node 写的，所以无论如何都需要一个 Node 进程。

  即梦（国内版）不需要浏览器通道，签名头齐全即可直连。

【常见问题】
  · 报 1015 login error
      账号集群与请求集群不匹配。检查账号的 store-idc 是否正确，
      以及添加时是否粘贴了完整的 Cookie。

  · 报 3018 permission denied
      浏览器通道没有正常工作。确认 sidecar 已启动、Chrome 已安装。

  · 报 shark not pass reject
      请求没有经过浏览器上下文。这类请求会被平台风控拦截，属预期行为。

  · 生成一直 pending / running
      看操作日志里的进度。图片通常几秒到几十秒；超时会在 5 分钟后失败。
