# 聚合网关

外部程序用 **baseURL + API Key** 接入，就能使用管理器的账号池出图。

协议做成 **OpenAI 兼容**——现成的客户端、工作流工具、官方 SDK 不用改代码就能接上，
把管理器当成一个图片生成服务用。

---

## 1. 接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/v1/images/generations` | 生成图片。默认同步等出图；`?async=1` 立即返回任务 id |
| GET | `/v1/images/generations/{id}` | 查询异步任务 |
| GET | `/v1/models` | 列出可用模型 |
| GET | `/v1/models/{id}` | 单个模型信息 |

鉴权：`Authorization: Bearer sk-dm-…`（也接受 `x-api-key` 头）。

这些路径**不经过**管理端的访问令牌校验——网关是给外部程序调用的，
和管理界面是两套身份。

## 2. 请求参数

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `prompt` | 是 | 提示词 |
| `model` | 否 | 默认 `high_aes_general_v50`（Seedream 5.0）。接受 `dall-e-3`、`gpt-image-1` 等别名并自动映射 |
| `size` | 否 | 如 `1024x1024`、`1792x1024`。**只用来决定宽高比** |
| `ratio` | 否 | 扩展字段，直接给比例：`1:1` `16:9` `9:16` `4:3` `3:4` `3:2` `2:3` `21:9` |
| `account_id` | 否 | 扩展字段，强制用某个账号 |
| `strategy` | 否 | 扩展字段，本次调用临时指定调度策略 |
| `n` | 否 | 接受但忽略——平台一次固定出 2 张 |

> **关于 size**：平台原生出图分辨率是 2048 长边。传 `1024x1024` 不会真的出 1024 的图，
> 只会按 1:1 出 2048。所以 `size` 只影响**宽高比**，不影响像素数。

## 3. 响应

同步模式返回 OpenAI 形状：

```json
{
  "created": 1790786145,
  "data": [
    { "url": "https://…", "revised_prompt": "…" }
  ],
  "task_id": "…",
  "model": "high_aes_general_v50"
}
```

`task_id` 是扩展字段，可以用它在管理器的「生成」页找到这次记录。

异步模式（`?async=1`）返回：

```json
{ "id": "…", "status": "pending", "object": "image.generation.task" }
```

错误沿用 OpenAI 信封：

```json
{ "error": { "message": "…", "type": "invalid_request_error", "code": "…", "param": null } }
```

## 4. 接入示例

```bash
curl http://127.0.0.1:8787/v1/images/generations \
  -H "Authorization: Bearer sk-dm-你的密钥" \
  -H "Content-Type: application/json" \
  -d '{"model":"high_aes_general_v50","prompt":"a ripe strawberry","size":"1024x1024"}'
```

```python
from openai import OpenAI

client = OpenAI(base_url="http://127.0.0.1:8787/v1", api_key="sk-dm-你的密钥")
result = client.images.generate(model="high_aes_general_v50", prompt="a ripe strawberry")
print(result.data[0].url)
```

## 5. 行为说明

- **走账号池调度**：网关不指定账号时，按管理器的调度策略（失败最少 / 轮询 / 积分最多）挑账号
- **进生成历史**：外部调用同样落库，在「生成」页能看到，日志里有 `gateway.request` 事件
- **失败换号**：账号失效会被标记 `expired` 并进入冷却，后续请求自动换下一个
- **超时**：默认同步等 180 秒（`GATEWAY_WAIT_TIMEOUT_MS`）。超时后任务仍在后台跑，
  可以用返回的 `task_id` 继续查

## 6. 密钥管理

在管理界面「接入」页创建，或者走管理端 API：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/keys` | 列出密钥（不含明文） |
| POST | `/api/keys` | 创建，**响应里带一次性明文** |
| PATCH | `/api/keys/{id}` | `{"enabled": false}` 吊销 |
| DELETE | `/api/keys/{id}` | 删除 |

密钥是 24 字节随机数（`sk-dm-` + 48 位 hex），库里只存 SHA-256。
因为是高熵随机值而非用户密码，不需要 bcrypt 这类慢哈希。

> 明文只在创建响应里出现一次，之后无法找回。丢了就重新建一把。
