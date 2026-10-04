# Dreamina（海外版）接口协议侦察记录

> 本文件记录的是**实测得到**的事实，以及明确标注的推断。
> 侦察时间：2026-09-21（初版）／2026-10-01（重大更正，见 §9）。
>
> ⚠️ **§1 的主机表与 §2 的签名公式在初版中是错的。§9 给出了用真实抓包逐字节验证过的
> 正确版本，请以 §9 为准。**

## 0. 一句话结论

**Dreamina 与即梦（jimeng.jianying.com）是同一套接口协议的海外部署**：相同的
`/mweb/v1/aigc_draft/generate`、相同的 `draft_content` 结构、相同的模型 req key。
差异只在**域名、aid、版本号、签名里的一个字段、以及账号体系**。

这意味着接入 Dreamina 相对即梦客户端是一个**小增量改动**，而不是重写。

---

## 1. 域名与常量（实测）

| 项 | Dreamina（海外） | 即梦（国内） |
| --- | --- | --- |
| 主站 | `dreamina.capcut.com` | `jimeng.jianying.com` |
| 业务 API | `https://mweb-api-sg.capcut.com` | `https://jimeng.jianying.com` |
| 电商 API | `https://commerce-api-sg.capcut.com` | 同主站 |
| 登录 passport | `dreamina.capcut.com/passport/web/`（另有 `login.us.capcut.com`、`login-row.www.capcut.com` 做兜底） | `jimeng.jianying.com/passport/` |
| `aid` | **513641** | 513695 |
| `web_version` | **7.5.0** | 7.5.0 |
| `da_version` | **3.3.28** | 3.3.20 |
| `pf` | 7 | 7 |
| 账号体系 | CapCut passport（Google / Apple / 邮箱 / TikTok） | 抖音/剪映 |

通用 query：`aid`、`device_platform=web`、`region=<JP|US|...>`、`web_id`、`web_version`、`da_version`、`aigc_features=app_lip_sync`。

---

## 2. 签名算法（实测，来自 `7373.*.js` 与 `5004.*.js`）

```js
// Dreamina 源码反混淆后的形态
const sign = md5([
  "9e2c",
  pathname.slice(-7),   // 只取路径最后 7 个字符
  pf,                   // "7"
  appvr,                // web_version，如 "7.5.0"
  Math.floor(Date.now()/1000),
  tdid,                 // 设备标识，缺省 "0"
  "11ac"
].join("|")).toLowerCase();

headers: { sign, "device-time": <同一个秒级时间戳> }
```

### 与即梦的关键差异

即梦（现有实现）：
```
md5("9e2c|" + uri.slice(-7) + "|7|5.8.0|" + ts + "||11ac")
                                              ^^ tdid 位置为空
```

Dreamina：
```
md5("9e2c|" + pathname.slice(-7) + "|7|" + appvr + "|" + ts + "|" + tdid + "|11ac")
                                                              ^^^^ 这里放 tdid
```

> **落地要点**：现有 `buildSign()` 只需把第 6 段从空串改成 `tdid`（默认 `"0"`），
> 并把 appvr 参数化即可复用。头部大小写无关。

---

## 3. 账号体系与地区

- 会话 cookie 落在 **`.capcut.com`**（不是 `.capcutapi.com`）。
- 地区查询：`POST /passport/web/region/?aid=513641&account_sdk_source=web&sdk_version=2.1.10-tiktok&language=en&verifyFp=<fp>`
  实测返回：`{"data":{"country_code":"sg","domain":"https://www.capcut.com"},"message":"success"}`
  注意 `country_code` 是**服务 IDC 归属**，不是用户国籍。
- 请求里会带 `region=JP` 之类的用户地区参数。
- 关键 cookie：`passport_csrf_token` / `passport_csrf_token_default`、`sessionid`、
  `store-region`、`ttwid`、`msToken`、`_tea_web_id`、`_v2_spipe_web_id`。
- 有 `x-tt-passport-csrf-token` 头部要求（写操作时从 cookie 里取 `passport_csrf_token`）。

---

## 4. 邮箱登录协议（实测）

**端点**
```
POST https://dreamina.capcut.com/passport/web/email/login/
     ?aid=513641&account_sdk_source=web&sdk_version=2.1.10-tiktok&language=en&verifyFp=<fp>
Content-Type: application/x-www-form-urlencoded
Origin: https://dreamina.capcut.com
```

**凭据混淆（关键）**：`mix_mode=1` 时，email 与 password **每个字节 XOR 0x05 后再转 hex**：

```js
const enc = (s) => { const b = Buffer.from(s, "utf8");
  for (let i=0;i<b.length;i++) b[i] ^= 0x05;
  return b.toString("hex"); };

// 实测校验：
//   enc("jimengceshi@163.com") === "6f6c68606b626660766d6c453433362b666a68"
//   enc("jimeng123")           === "6f6c68606b62343736"
```

**请求体**
```
mix_mode=1&email=<hex>&password=<hex>&fixed_mix_mode=1
```

**错误码（实测）**

| code | 含义 |
| --- | --- |
| 0 | 成功 |
| 1011 | `Account doesn't exist` |
| 16 | `Application has no permissions`（aid 用错时出现，例如 aid=1988 / capcut.cn） |

> 注意：**1011 对「肯定不存在的邮箱」与「待测邮箱」返回完全一致**，
> 因此它也可能是风控下的通用兜底响应，不能 100% 断定为「未注册」。

**浏览器内的坑**：官方 SDK 会把登录请求发到 `www.capcut.com`（跨域），
在无头浏览器里会因 CORS 失败（`net::ERR_FAILED`）。
**绕过办法：直接从 Node 调 passport 接口**，不受 CORS 约束——已实测可行。

---

## 5. 业务接口清单（实测抓包 + bundle 提取）

```
POST /mweb/v1/aigc_draft/generate          ← 提交生成（与即梦同名同构）
POST /mweb/v1/get_history_by_ids           ← 轮询结果
POST /mweb/v1/feature_permission           ← 功能开关（no-use-permission 的判定来源）
POST /mweb/v1/get_common_config
POST /mweb/v1/video_generate/get_common_config
POST /mweb/v1/audio_generate/get_common_config
POST /mweb/v1/capability/capsules
POST /mweb/v1/creation_agent/v2/get_agent_config
POST /mweb/v1/creation_agent/v2/skill/list
POST /mweb/v1/creation_agent/v2/conversation
POST /mweb/v1/dreamina_subject/get
POST /mweb/v1/mpack_image
POST /mweb/v1/blend_preview
POST /mweb/v1/face_recognize
POST /mweb/v1/algo_proxy
POST /mweb/v1/get_user_local_item_list
POST /mweb/v1/get_explore_category_list
POST /mweb/v1/batch_collect
POST /lv/v1/user/get_enable_list
POST /commerce/v1/resource_position
```

`/mweb/v1/feature_permission` 请求样例（未登录时也会调用）：
```json
{"feature_keys":["dreamina_video_generate","dreamina_canvas_preview","dreamina_image_generate",
 "human_face_enable","dreamina_story_mode","dreamina_workflow_mode","dreamina_story_agent",
 "dreamina_audio_generate","bg_paint_enable","canvas_fusion_enable","dreamina_smart_reference",
 "dreamina_lip_sync","mweb_publish_image","mweb_publish_video"]}
```
响应按 `{ret, errmsg, data}` 信封返回，与即梦一致。

---

## 6. 模型 req key（bundle 提取，与即梦同源）

**图片**：`high_aes_general_v50p_large`、`high_aes_general_v50`、`high_aes_general_v43`、
`high_aes_general_v42`、`high_aes_general_v41`、`high_aes_general_v40`、
`high_aes_general_v30l_art:general_v3.0_18b`（显示名 "Image 3.1"）…

**视频**：`dreamina_seedance_45_pro`、`dreamina_seedance_40_pro`、`dreamina_seedance_40`、
`dreamina_seedance_40_mini`、`dreamina_seedance_40_vision`、
`basic_video_operation_vgfm_v_three*` 等。

**结构体**：`DAText2VideoParams`（含 `video_gen_inputs`、`video_aspect_ratio`、`model_req_key`）、
`DAVideoGenInput`、图片侧 `core_param` + `ability_list`（`ByteEdit` 能力名与即梦一致）。

---

## 7. 实测验证结果（2026-09-21，使用一个真实登录态）

用一个有效的 `sessionid` 实测，结论如下。

### ✅ 已验证可用

| 能力 | 结果 |
| --- | --- |
| 会话有效性 | 带 cookie `ret:"0"`，不带 cookie `ret:"1015" login error` → **cookie 有效** |
| 签名算法 | `/mweb/v1/*` 全部通过（`feature_permission`、`get_common_config`、`dreamina_subject/get` 均 `ret:"0"`） |
| 积分查询 | `POST https://commerce-api-sg.capcut.com/commerce/v1/benefits/user_credit` 返回 `ret:"0"` |
| 模型目录 | `/mweb/v1/get_common_config` 返回 41KB，含全部 `model_req_key` |

拉到的图片模型 req key（与即梦同源）：
```
high_aes_general_v50p_large   high_aes_general_v50    high_aes_general_v43
high_aes_general_v42          high_aes_general_v40l   high_aes_general_v41
high_aes_general_v40          high_aes_general_v30l_art:general_v3.0_18b
high_aes_general_v30l:general_v3.0_18b
dreamina_lib_img_20260423     external_model_gemini_flash_image_v25   ← 海外版独有
```

### ⚠️ 关键架构发现：必须走浏览器上下文

**同样的请求，从 Node 直发 vs 从浏览器页面内发，结果完全不同**：

| 发起方 | 结果 |
| --- | --- |
| Node + undici + 正确签名 + 正确 cookie | `ret:"-6" shark not pass reject`<br>`fail_starling_message: "Couldn't generate due to unusual activity in your account."` |
| 浏览器页面内 `fetch()` | Shark 通过 → `ret:"3018" permission denied`（fail_code 1017） |

**结论**：Dreamina 对**写操作**（`aigc_draft/generate`、`credit_receive`）强制校验
`msToken` / `X-Bogus` / `X-Gnarly` 等浏览器端风控签名（由 secsdk 在页面里生成）。
读操作（`feature_permission`、`get_common_config`、`subject/get`、`user_credit`）不需要。

> 这一点与即梦**根本不同**：即梦只要签名对就行，Dreamina 必须借助真实浏览器上下文。
> 纯服务端 HTTP 的适配器在 Dreamina 上**不可行**。

### ❌ 功能权限（`/mweb/v1/feature_permission` 返回）

逐个 feature key 查询，**只有一项被限制**：

| feature_key | 结果 |
| --- | --- |
| `dreamina_video_generate` | **`status: 3`**，`is_white: false`，`is_invite: false`，`name: "Dreamina视频生成-海外"`，附 `questionnaire_url`（申请问卷） |
| 其余全部（image_generate / canvas / story_mode / workflow / audio / lip_sync / smart_reference / human_face / bg_paint …） | 无限制配置 = 默认开放 |

**这就是 `/no-use-permission` 的直接来源**：视频生成功能对该账号未开放（status=3，需申请或白名单）。
`NoUsePermission:"/no-use-permission"` 是 `main.*.js` 里的客户端路由，由该接口返回驱动。

### ❌ 账号本身无生成权限

即便 Shark 已通过、且 `dreamina_image_generate` 显示「默认开放」，
提交文生图仍然返回：

```json
{"ret":"3018","errmsg":"permission denied",
 "data":{"aigc_data":null,"fail_code":"1017"}}
```

且积分恒为 0：
```json
{"credit":{"vip_credit":0,"gift_credit":0,"purchase_credit":0}}
```
`credit_receive` 返回 `is_first_receive:false, receive_quota:0` —— 没有可领的免费额度。

**判断**：该账号在 Dreamina 侧**没有生成权限**，与代理 IP、签名、风控均无关，是**账号归属地/资质**问题。
这与「海外 IP ≠ 海外注册用户」一致：用大陆邮箱（@163.com）注册/关联的账号，
在海外版拿不到生成额度。

---

## 8. 尚未验证 / 待办（初版，部分已被 §9 推翻）

- **端到端生成未跑通**：受测账号 `3018 permission denied`，无法验证提交→轮询→取图全链路。
  提交请求本身已被服务端**正常解析**（走到了权限校验，而非签名/格式错误），说明 payload 结构正确。
- **轮询状态码**：只确认了即梦那套（20/42/45 处理中、30 失败、21/50 完成）在同源代码里存在，
  未在 Dreamina 真机验证。
- **`/lv/v1/*` 的签名方式不同**：`get_enable_list` 无论 `appvr` 取何值都返回
  `1014 sign error`，说明该前缀另有签名规则（可能必须带 msToken）。不影响主流程。
- **`external_model_gemini_flash_image_v25`**：海外版接了第三方 Gemini 图像模型，
  其调用路径未探查。
- `verifyFp` 是否需要真实指纹：实测用随机生成的 `verify_*` 串即可通过，说明服务端不严格校验。

---

# 9. 实测确认（2026-10-01，用一个可正常生成视频的美国区账号）

本节全部结论来自**真实抓包与可复现实测**，覆盖并修正前文的推断。

## 9.1 ⭐ 签名公式：逐字节验证通过

用浏览器抓到的真实请求反推，**精确命中**：

```
device-time = 1790786145
path        = /mweb/v1/aigc_draft/generate   → 后 7 位 = "enerate"
观测 sign   = 2533662377211f0653b7759901657a92

md5("9e2c|enerate|7|8.4.0|1790786145||11ac") = 2533662377211f0653b7759901657a92  ✅
```

**正确公式**（与即梦完全同构，仅 appvr 取值不同）：

```js
sign = md5(["9e2c", pathname.slice(-7), "7", appvr, unixSeconds, "", "11ac"].join("|")).toLowerCase()
//                                                       ^^ tdid 是空串
headers: { sign, "device-time": String(unixSeconds), "sign-ver": "1" }
```

初版的三处错误：
- `appvr` = **`8.4.0`**，取自请求头 `appvr`，**不是** `web_version`(7.5.0)
- `tdid` = **空串**，不是 `"0"`
- 第 3 段固定 `"7"`（对应请求头 `pf`）

对照实验跑了 16 种组合，只有上述一种命中。

## 9.2 ⭐ API 主机按地区划分（`1015 login error` 的真正原因）

同一个 sessionid、同一份代码：

| 主机 | region | 结果 |
| --- | --- | --- |
| `dreamina-api-us-ttp2.us.capcut.com` | `US` | **`ret=0` success** ✅ |
| `mweb-api-sg.capcut.com` | `US` | `1015 login error` ❌ |
| `mweb-api-sg.capcut.com` | `JP` | `1015 login error` ❌ |

**账号归属哪个集群就必须打哪个主机，且 `region` 参数要一致。** 之前所有的鉴权失败都源于此。

美国区完整主机清单（实测）：

```
dreamina-api-us-ttp2.us.capcut.com     主业务 API
dreamina-api.us.capcut.com             备用
commerce-us-ttp2.us.capcut.com         积分 / 订阅 / 电商
web-edit-us-ttp2.us.capcut.com
```

命名规律 `{service}-{region-ttp2}.{region}.capcut.com`；新加坡区是 `mweb-api-sg.capcut.com`。
账号归属可从 cookie `store-idc`（`useast5`）和 `store-country-code`（`us`）读出。

## 9.3 读操作可纯 Node，写操作必须借浏览器

| 操作 | Node 直发 | 页面内 `fetch()` |
| --- | --- | --- |
| 读（`dreamina_subject/get`、`get_common_config`、`feature_permission`、`workspace/list`、`user_credit`） | ✅ | ✅ |
| 写（`aigc_draft/generate`） | ❌ `ret=-6 shark not pass reject` | ✅ 通过风控 |

页面内实测：`window.byted_acrawler` 存在，且 secsdk **给 `fetch` 和 `XMLHttpRequest` 都打了补丁**
（两者的 `toString()` 都不含 "native code"），所以页面内请求自动带上：

```
&msToken=I5YVBVqY-...&X-Bogus=DFSzswjLIjSewOFYCfqCpFVMz3ZZ&X-Gnarly=MkQ6l1Un-...
```

这三个参数在 **URL query** 里，不在请求头，Node 无法自行生成。

> **结论：Dreamina 适配器必须走浏览器上下文（Playwright）。纯服务端 HTTP 不可行。**

## 9.4 积分

```
POST https://commerce-us-ttp2.us.capcut.com/commerce/v1/benefits/user_credit
→ {"ret":"0","response":"{\"credit\":{\"vip_credit\":0,\"gift_credit\":136,\"purchase_credit\":0},...}"}
```

**读积分可以纯 Node**。注意返回体是嵌套 JSON 字符串，`response` 字段需二次 `JSON.parse`。

## 9.5 ⚠️ 更正：`feature_permission` 的 `status: 3` 不代表被封

初版把 `status: 3` 解读为「功能未开放」，**这是错的**。用一个确认能生成视频的账号实测，
**所有** feature 都返回 `status: 3`：

```json
{"feature_key":"dreamina_image_generate","name":"Dreamina文生图","status":3,"is_white":false,"is_invite":false}
{"feature_key":"dreamina_video_generate","name":"Dreamina视频生成-海外","status":3,"is_white":false,"is_invite":false}
```

`status: 3` 只是配置项的一种状态，**不能**用来判断可用性。

## 9.6 视频生成：payload 结构已验证

用抓到的成功请求（`dreamina_seedance_40_mini` + `m_video_commerce_info`）原样重放，
**越过了权限校验**，停在积分/权益检查：

```json
{"ret":"1006","errmsg":"Not enough credits left or no relevant benefits",
 "data":{"fail_code":"1006","fail_starling_key":"web_dre_m10n_generate_popup_credit_ininsufficient_title"}}
```

说明 `draft_content`（`video_base_component`/`gen_video`/`text_to_video_params`）与
`extend.m_video_commerce_info` 结构**正确**；该免费账号只是缺视频对应的付费权益
（赠送积分不能用于视频，需要 `benefit_type: seedance_20_mini_720p_output_8s` 对应的订阅）。

视频必备字段（抓包确认）：

```json
"extend": {
  "root_model": "dreamina_seedance_40_mini",
  "m_video_commerce_info": { "amount": 5, "benefit_type": "seedance_20_mini_720p_output_8s",
                             "resource_id": "generate_video", "resource_id_type": "str", "resource_sub_type": "aigc" },
  "workspace_id": 249443284237,
  "m_video_commerce_info_list": [ <同上> ]
}
```

URL 另需 `&commerce_with_input_video=1&generate_id=gen-<uuid>&babi_param=<编码埋点>`。

## 9.7 ❌ 未解决：图片生成返回 `3018 permission denied`

```json
{"ret":"3018","errmsg":"permission denied","data":{"aigc_data":null,"fail_code":"1017"}}
```

已排除（都试过，结果一致）：
- 主机：`dreamina-api-us-ttp2.us.capcut.com` 与 `dreamina-api.us.capcut.com` 都试了
- 模型：`v50p_large` / `v41` / `v40` / `v30l` 都一样
- commerce：带正确 `m_image_commerce_info`（`image_basic_v50_pro_1k` 等）与不带，一样
- `workspace_id`：加与不加，一样

**待确认**：该账号是否本来就不能生成图片（用户只验证过视频）。
需要在浏览器里**手工生成一张图**确认：若浏览器也失败，则 `3018` 是账号权益问题；
若浏览器成功，说明还缺字段，需 UI 抓包逐个对比。

## 9.8 其他可用接口

```
POST /mweb/v1/workspace/list      → {"data":{"workspaces":[{"workspace_id":249443284237,"name":"..."}]}}
POST /mweb/v1/get_history_by_ids  → 轮询结果（结构同即梦）
```

## 9.9 美国区模型清单（get_common_config）

```
high_aes_general_v50p_large   high_aes_general_v50    high_aes_general_v42
high_aes_general_v40l         high_aes_general_v41    high_aes_general_v40
high_aes_general_v30l:general_v3.0_18b
dreamina_gpt_image_2_5_flare  dreamina_gpt_image_2_5_sunburst
gpt_image_2                   gemini_3_image_pro      ← 美国区接入的第三方模型
```

图片 benefit key 形如 `image_basic_{v50_pro|v5|v46|v4_pro|v41}_{1k|1.5k|2k|4k}`、
`image_basic_gpt_image_v2*_*`、`image_basic_banana_pro_*`（Gemini "banana"）。

---

# 10. ✅ 最终可用方案（2026-10-01 全链路跑通并验证）

**结论：文生图已端到端跑通** —— 提交 `ret:"0"`、轮询 `status:50`、下载到 2048×2048 成图。

## 10.1 ⭐ 核心结论：两个条件必须同时满足

这是整件事最难的地方，也是之前反复失败的原因：

| | 风控（shark） | 权限（3018/1017） |
| --- | --- | --- |
| **Node 直发 + 完整签名头** | ❌ 被拦 | — |
| **页面内 fetch，只带 content-type** | ✅ 通过 | ❌ `3018 permission denied` |
| **页面内 fetch + 完整签名头** | ✅ 通过 | ✅ **`ret:"0"`** |

- **风控**要的是 secsdk 自动加在 URL 上的 `msToken`/`X-Bogus`/`X-Gnarly` → 只能在页面上下文里发
- **权限**要的是 `sign`/`device-time`/`appid`/`appvr`/`pf`/`loc` 等请求头 → **页面内裸 `fetch` 不会自动带，必须手工加**

> 我最初从页面里发请求时只设了 `content-type`，所以一直卡在 3018。
> 补上签名头之后立刻 `ret:"0"`。

## 10.2 完整请求头（图片/视频通用）

```js
{
  "content-type": "application/json",
  "accept": "application/json, text/plain, */*",
  "accept-language": "zh-CN,zh;q=0.9",
  "app-sdk-version": "48.0.0",
  "appid": "513641",
  "appvr": "8.4.0",
  "pf": "7",
  "sign-ver": "1",
  "sign": md5("9e2c|" + path.slice(-7) + "|7|8.4.0|" + ts + "||11ac"),
  "device-time": String(ts),      // 与 sign 同一个秒级时间戳
  "lan": "en",
  "loc": "US",                    // 与账号地区一致
  "store-country-code": "us",
  "store-country-code-src": "uid",
  "tdid": ""                      // 空值
}
```

## 10.3 文生图 payload（照真实抓包，已跑通）

```jsonc
// URL query
//   aid=513641&device_platform=web&region=US&da_version=3.3.28&os=windows
//   &web_component_open_flag=1&web_version=7.5.0&aigc_features=app_lip_sync
//   &generate_id=gen-<uuid>&babi_param=<双重URL编码的JSON>&commerce_with_input_video=1
{
  "extend": {
    "root_model": "high_aes_general_v50",   // 注意：extend 里【没有】commerce info
    "workspace_id": 501633082374            // 必须是最新的 workspace（用 /mweb/v1/workspace/list 取）
  },
  "submit_id": "<uuid>",
  "metrics_extra": "<字符串>",              // 见下，注意【没有 sceneOptions】
  "draft_content": "<字符串>",
  "http_common_info": { "aid": 513641 }
}
```

`metrics_extra`（解码后）——**图片路径没有 `sceneOptions`**：
```json
{
  "promptSource": "custom", "generateCount": 1, "enterFrom": "click",
  "position": "page_bottom_box", "isBoxSelect": false, "isCutout": false,
  "hasRejectedAudit": 0, "generateId": "<同 submit_id>", "isRegenerate": false
}
```

`draft_content`（解码后）：
```json
{
  "type": "draft", "id": "<uuid>", "min_version": "3.0.2", "min_features": [],
  "is_from_tsn": true, "version": "3.3.28",
  "main_component_id": "<componentId>",
  "component_list": [{
    "type": "image_base_component", "id": "<componentId>", "min_version": "3.0.2",
    "aigc_mode": "workbench",
    "metadata": { "type": "", "id": "<uuid>", "created_platform": 3,
                  "created_platform_version": "", "created_time_in_ms": "<字符串>", "created_did": "" },
    "generate_type": "generate",
    "abilities": { "type": "", "id": "<uuid>",
      "generate": { "type": "", "id": "<uuid>",
        "core_param": {
          "type": "", "id": "<uuid>", "model": "high_aes_general_v50",
          "prompt": "<提示词>", "negative_prompt": "", "seed": 3301617703,
          "sample_strength": 0.5, "image_ratio": 1,
          "large_image_info": { "type": "", "id": "<uuid>", "height": 2048, "width": 2048, "resolution_type": "2k" },
          "intelligent_ratio": false, "generate_type": 0
        } } }
  }]
}
```

### 与我最初写法的差异（逐条都是踩过的坑）

| 字段 | 错误写法 | 正确写法 |
| --- | --- | --- |
| `extend` | 带 `m_image_commerce_info` | **只留 `root_model` + `workspace_id`** |
| `metrics_extra` | 带 `sceneOptions` | **不带**；要带 `position`、`hasRejectedAudit` |
| `draft_content.version` | `"3.0.2"` | **`"3.3.28"`**（`min_version` 才是 3.0.2） |
| component | 有 `gen_type: 1` | **没有** |
| `core_param` | 无 `intelligent_ratio` | **有 `intelligent_ratio: false`** |
| `abilities.generate` | 有 `history_option` | **没有** |
| URL | 只有 `generate_id` | 还要 `babi_param` + `commerce_with_input_video=1` |
| 主机 | `dreamina-api-us-ttp2...` | `dreamina-api.us.capcut.com` 也可（两个都能用） |

## 10.4 轮询取图

```
POST /mweb/v1/get_history_by_ids   {history_ids:[hid], http_common_info:{aid:513641}}
```

- 响应是**以 history_id 为键的 map**（无 `ret` 信封）
- 状态：`20/42/45` 处理中，`50` 完成（实测 5 秒内即完成），`30` 失败
- 图片 URL：`item_list[*].image.large_images[0].image_url`，回退 `common_attr.cover_url`
- **读操作 Node 直发即可**（不需要浏览器）

实测结果：`status=50`，返回 2 张 2048×2048 JPEG，可直接下载。

## 10.5 落地到适配器

```
┌─ 读操作（积分/模型/workspace/轮询）──→ 纯 Node + undici + ProxyAgent
└─ 写操作（提交生成）────────────────→ Playwright 页面上下文
                                        · 注入 sessionid cookie
                                        · page.evaluate 里 fetch
                                        · 手工带上全部签名头
```

浏览器只需**常驻一个页面**用于计算 `msToken`/`X-Bogus`/`X-Gnarly`（secsdk 自动加），
其余全部走 Node，开销很小。

