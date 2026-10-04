# Jimeng (即梦) Generation API — Reverse-Engineered Reference

Source: **zhizinan1997/jimeng-free-api-all** @ branch `main`
Files read (downloaded to `.research/A/`):

| File | Role |
|---|---|
| `src/api/controllers/core.ts` (596 lines) | HTTP plumbing: fake headers, cookie, **sign**, retries, file upload, credit |
| `src/api/controllers/images.ts` (729 lines) | Text-to-image + reference-image (blend) generation, polling |
| `src/api/controllers/videos.ts` (788 lines) | Text-to-video + first/last-frame video, polling |
| `src/api/controllers/models.ts` (815 lines) | Static model registry, req-keys, benefit counts |
| `src/api/routes/images.ts`, `src/api/routes/videos.ts` | Public OpenAI-compatible surface |
| `src/lib/util.ts` | `uuid()`, `md5()`, `unixTimestamp()`, `crc32()` |
| `API.md` | Official-in-repo API doc (credit table, error codes) |

Upstream host throughout: **`https://jimeng.jianying.com`** (core.ts:152).

---

## 0. TL;DR of the wire protocol

1. Upload any reference/first-frame images → get `image_uri` strings (ImageX).
2. `POST /mweb/v1/aigc_draft/generate` with a **two-level-JSON** body (`draft_content` and `metrics_extra` are *strings*, not objects) → returns `data.aigc_data.history_record_id`.
3. Poll `POST /mweb/v1/get_history_by_ids` (image & video) — or `POST /mweb/v1/get_history_records` for video after 10 retries — until `status` leaves `{20,42,45}` and `item_list` is non-empty.
4. Read the finished asset URL out of `item_list[*]`.

Every request carries `Device-Time`, `Sign`, `Sign-Ver: 1` and a synthesized `Cookie` — see §2.

---

## 1. Model keys and what they map to

### 1.1 Image models

`MODEL_MAP` in images.ts:21-32 (public id → upstream `model` req-key). `models.ts:59-170` adds the display name / supported resolutions / benefit counts.

| Public model id | Upstream `model` / `model_req_key` | Display name | Supported resolutions | Default |
|---|---|---|---|---|
| `jimeng-image-5.0-pro` | `high_aes_general_v50p_large` | Seedream 5.0 Pro | `4k`,`2k`,`1.5k` | `2k` |
| `jimeng-image-5.0-lite` | `high_aes_general_v50` | Seedream 5.0 Lite | `4k`,`2k` | `2k` |
| `jimeng-image-4.7` | `high_aes_general_v43` | Seedream 4.7 | `4k`,`2k` | `2k` |
| `jimeng-image-4.6` | `high_aes_general_v42` | Seedream 4.6 | `4k`,`2k` | `2k` |
| `jimeng-image-4.5` | `high_aes_general_v40l` | Seedream 4.5 | `4k`,`2k` | `2k` |
| `jimeng-image-4.1` | `high_aes_general_v41` | Seedream 4.1 | `4k`,`2k` | `2k` |
| `jimeng-image-4.0` | `high_aes_general_v40` | Seedream 4.0 | `4k`,`2k` | `2k` |
| `jimeng-image-3.1` | `high_aes_general_v30l_art_fangzhou:general_v3.0_18b` | Seedream 3.1 | `1k` | `1k` |
| `jimeng-image-3.0` | `high_aes_general_v30l:general_v3.0_18b` | Seedream 3.0 | `1k` | `1k` |
| `jimeng-image-2.0-pro` | `high_aes_general_v20_L:general_v2.0_L` | Seedream 2.0 Pro | `1k` | `1k` |

Default when `model` is absent: `jimeng-image-5.0-lite` (images.ts:11), resolved through `getModel()` which **silently falls back** to the default for unknown ids (images.ts:180-182).

`isHighResImageModel()` (images.ts:34-44) = the set `{5.0-lite, 4.7, 4.6, 4.5, 4.1, 4.0}` → those accept `2k`/`4k`; everything else is `1k`.

### 1.2 Video models

`MODEL_MAP` in videos.ts:16-35. Note there is **no `video_mode`/`seedance 1.0`** naming here — "seedance" ids map onto `dreamina_seedance_*` / `dreamina_ic_generate_video_model_vgfm_*` req-keys.

| Public model id | Upstream `model_req_key` | Supported res | Long duration? | Durations (s) |
|---|---|---|---|---|
| `jimeng-video-seedance-2.5` | `dreamina_seedance_45_pro` | `720p` | yes | 5/10 (no explicit list) |
| `jimeng-video-seedance-2.0-mini` | `dreamina_seedance_40_mini` | `720p` | yes | 4…15 |
| `jimeng-video-seedance-2.0-fast` (`seedance-2.0-fast`) | `dreamina_seedance_40` | `720p` | yes | 4…15 |
| `jimeng-video-seedance-2.0` (`seedance-2.0`,`seedance-2.0-pro`,`jimeng-video-seedance-2.0-pro`) | `dreamina_seedance_40_pro` | `720p` | yes | 4…15 |
| `jimeng-video-seedance-2.0-fast-vip` (`seedance-2.0-fast-vip`) | `dreamina_seedance_40_vision` | `720p` | yes | 4…15 |
| `jimeng-video-seedance-2.0-vip` (`seedance-2.0-vip`) | `dreamina_seedance_40_pro_vision` | `720p`,`1080p`,`4k` | yes | 4…15 |
| `jimeng-video-seedance-1.5-pro` | `dreamina_ic_generate_video_model_vgfm_3.5_pro` | `720p` | yes | 5/10 |
| `jimeng-video-3.0-pro` | `dreamina_ic_generate_video_model_vgfm_3.0_pro` | `1080p` | yes | 5/10 |
| `jimeng-video-3.0` | `dreamina_ic_generate_video_model_vgfm_3.0` | `720p` | yes | 5/10 |
| `jimeng-video-3.0-fast` | `dreamina_ic_generate_video_model_vgfm_3.0_fast` | `720p`,`1080p` | yes | 5/10 |
| `jimeng-video-s2.0` | `dreamina_ic_generate_video_model_vgfm_lite` | `720p` | **no** | 5 only |
| `jimeng-video-2.0-pro` | `dreamina_ic_generate_video_model_vgfm1.0` | `720p` | **no** | 5 only |

Constants: `SEEDANCE_2_DURATIONS = [4,5,6,7,8,9,10,11,12,13,14,15]` (videos.ts:15), `DEFAULT_VIDEO_DURATIONS = [5,10]` (videos.ts:14). Default video model: `jimeng-video-seedance-2.0` (videos.ts:11). `getModel()` falls back to the default for unknown ids (videos.ts:37-39).

### 1.3 Aspect-ratio enums

**Image** (images.ts:76-97) — the string is the public enum, the number is what goes into `core_param.image_ratio`:

| ratio string | `image_ratio` |
|---|---|
| `21:9` | 0 |
| `16:9` | 1 |
| `3:2` | 2 |
| `4:3` | 3 |
| `1:1` | **8** (not 4!) |
| `3:4` | 4 |
| `2:3` | 5 |
| `9:16` | 6 |

**Video** (videos.ts:271): `["16:9","9:16","1:1","4:3","3:4","21:9"]` — sent as the *string* in `video_aspect_ratio`, not a number. Default `16:9`.

Both accept prompt-based detection: a `(\d+)[:：](\d+)` regex plus the keywords `横屏|横版|宽屏`→16:9, `竖屏|竖版|手机`→9:16, `方形|正方`→1:1 (images.ts:150-178, videos.ts:277-305).

### 1.4 Resolution → pixel dimensions (images only)

`large_image_info.width/height` is an **explicit pixel size**, not just a level. Four tables (images.ts:100-143):

| ratio | 1k | 1.5k | 2k | 4k |
|---|---|---|---|---|
| 21:9 | 2016×846 | 2268×972 | 3024×1296 | 6197×2656 |
| 16:9 | 1664×936 | 1920×1080 | 2560×1440 | 5404×3040 |
| 3:2 | 1584×1056 | 1872×1248 | 2496×1664 | 4992×3328 |
| 4:3 | 1472×1104 | 1728×1296 | 2304×1728 | 4693×3520 |
| 1:1 | 1328×1328 | 1536×1536 | 2048×2048 | 4096×4096 |
| 3:4 | 1104×1472 | 1296×1728 | 1728×2304 | 3520×4693 |
| 2:3 | 1056×1584 | 1248×1872 | 1664×2496 | 3328×4992 |
| 9:16 | 936×1664 | 1080×1920 | 1440×2560 | 3040×5404 |

### 1.5 Video duration / resolution normalization

* `getSupportedVideoDurations()` (videos.ts:188-196): explicit `supportedDurations` if set, else `[5,10]` when `supportsLongDuration`, else `[5]`.
* `getDefaultVideoDuration()` (videos.ts:198-204): `defaultDuration` (5 for all Seedance 2.x) or `10` if supported, else the smallest.
* Invalid `duration` → silently replaced by the default; if `duration` was **omitted** and the default is 10, the prompt is scanned for `10\s*[秒sS]` then `5\s*[秒sS]` (videos.ts:312-323, 404-415).
* `duration_ms = finalDuration * 1000` (videos.ts:417).
* Unsupported `resolution` → replaced by `modelConfig.defaultResolution` (videos.ts:355-360).

---

## 2. Required headers, cookie, and the sign string

### 2.1 Static / fake headers (core.ts:32-53, 161-168)

```js
const FAKE_HEADERS = {
  Accept: "application/json, text/plain, */*",
  "Accept-Encoding": "gzip, deflate, br, zstd",
  "Accept-language": "zh-CN,zh;q=0.9",
  "Cache-control": "no-cache",
  Appid: `${DEFAULT_ASSISTANT_ID}`,   // "513695"
  Appvr: VERSION_CODE,                 // "5.8.0"
  Origin: "https://jimeng.jianying.com",
  Pragma: "no-cache",
  Priority: "u=1, i",
  Referer: "https://jimeng.jianying.com",
  Pf: PLATFORM_CODE,                   // "7"
  "Sec-Ch-Ua": '"Google Chrome";v="142", "Chromium";v="142", "Not_A Brand";v="24"',
  "Sec-Ch-Ua-Mobile": "?0",
  "Sec-Ch-Ua-Platform": '"Windows"',
  "Sec-Fetch-Dest": "empty",
  "Sec-Fetch-Mode": "cors",
  "Sec-Fetch-Site": "same-origin",
  "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36",
};
```

Per-request merged headers (core.ts:161-168):

```js
const headers = {
  ...FAKE_HEADERS,
  Cookie: generateCookie(token),
  "Device-Time": deviceTime,
  Sign: sign,
  "Sign-Ver": "1",
  ...(options.headers || {}),
};
```

### 2.2 The sign string (core.ts:147-150)

```js
const deviceTime = util.unixTimestamp();            // parseInt(Date.now()/1000)  → 10-digit seconds
const sign = util.md5(
  `9e2c|${uri.slice(-7)}|${PLATFORM_CODE}|${VERSION_CODE}|${deviceTime}||11ac`
);
```

Concretely, for `uri = "/mweb/v1/aigc_draft/generate"`:

```
sign = md5("9e2c|" + "generate"                      // uri.slice(-7) == last 7 chars
         + "|" + "7"                                 // PLATFORM_CODE
         + "|" + "5.8.0"                             // VERSION_CODE
         + "|" + deviceTime                          // 10-digit unix seconds
         + "|" + "" + "|" + "11ac")                  // note the empty segment + "11ac"
```

* `uri.slice(-7)` — for `/mweb/v1/get_history_by_ids` → `"by_ids"`; for `/mweb/v1/get_history_records` → `"records"`; for `/commerce/v1/benefits/user_credit` → `"credit"` (the tail-7 of the **path without query string**).
* `md5` is plain hex MD5 (util.ts:275-277).
* `Device-Time` and the timestamp inside the sign must be the same value.
* `Sign-Ver: "1"`.

### 2.3 Cookie synthesis (core.ts:71-85)

The "login token" **is** the jimeng `sessionid`; the repo never refreshes it (`acquireToken` just returns it, core.ts:64-66). A full cookie jar is fabricated around it:

```js
export function generateCookie(refreshToken: string) {
  return [
    `_tea_web_id=${WEB_ID}`,
    `is_staff_user=false`,
    `store-region=cn-gd`,
    `store-region-src=uid`,
    `sid_guard=${refreshToken}%7C${util.unixTimestamp()}%7C5184000%7CMon%2C+03-Feb-2025+08%3A17%3A09+GMT`,
    `uid_tt=${USER_ID}`,
    `uid_tt_ss=${USER_ID}`,
    `sid_tt=${refreshToken}`,
    `sessionid=${refreshToken}`,
    `sessionid_ss=${refreshToken}`,
    `sid_tt=${refreshToken}`
  ].join("; ");
}
```

With (core.ts:22-26): `WEB_ID = Math.random()*1e18 + 7e18` (a **float**, serialized as e.g. `7.34e+18`), `USER_ID = util.uuid(false)` (uuid without dashes). `DEVICE_ID`, `MODEL_NAME`, `MAX_RETRY_COUNT`, `RETRY_DELAY` are declared but unused.

### 2.4 Query-string base params (core.ts:152-159)

```js
const fullUrl = `https://jimeng.jianying.com${uri}`;
const requestParams = {
  aid: DEFAULT_ASSISTANT_ID,      // 513695
  device_platform: "web",
  region: "CN",
  webId: WEB_ID,                  // the float above
  ...(options.params || {}),
};
```

Axios options: `timeout: 45000`, `validateStatus: () => true`, up to 3 retries on HTTP ≥400 / ECONNABORTED / ETIMEDOUT / "timeout" / "network" with linear backoff `1000*retries` ms (core.ts:187-234).

### 2.5 Response envelope + error mapping (core.ts:549-561)

```js
export function checkResult(result: AxiosResponse) {
  const { ret, errmsg, data } = result.data;
  if (!_.isFinite(Number(ret))) return result.data;   // e.g. get_history_by_ids returns a map, not an envelope
  if (ret === "0") return data;                       // success → unwrap data
  if (ret === "5000" || ret === "1006")
    throw new APIException(EX.API_IMAGE_GENERATION_INSUFFICIENT_POINTS, ...);  // insufficient credits
  throw new APIException(EX.API_REQUEST_FAILED, ...);
}
```

So: **`{"ret":"0","data":{...}}`** is the success envelope for `aigc_draft/generate`, `user_credit`, `credit_receive`, upload-token, etc. `/mweb/v1/get_history_by_ids` returns a bare map (no `ret`) and is passed through untouched.

---

## 3. Credits (积分)

Constants in both controllers: `const DEFAULT_ASSISTANT_ID = 513695;` (images.ts:10, videos.ts:10).

### 3.1 Query + claim

```js
// core.ts:92-111
POST /commerce/v1/benefits/user_credit     body {}
  → data.credit.{gift_credit, purchase_credit, vip_credit}
  → { giftCredit, purchaseCredit, vipCredit, totalCredit: gift+purchase+vip }

// core.ts:118-130
POST /commerce/v1/benefits/credit_receive  body { "time_zone": "Asia/Shanghai" }
  → { cur_total_credits, receive_quota }
```

Both generation paths do this immediately before submitting (images.ts:295-296, videos.ts:424-425):

```js
const { totalCredit } = await getCredit(refreshToken);
if (totalCredit <= 0) await receiveCredit(refreshToken);
```

### 3.2 Cost table

From `API.md` §7 (credits **per image**, `n` multiplies):

| Model | Resolution | Credits / image |
|---|---|---|
| `jimeng-image-3.1` / `3.0` | 1K | 1 |
| `jimeng-image-5.0-lite` | 2K | 3 |
| `jimeng-image-4.7` / `4.6` / `4.5` / `4.1` / `4.0` | 2K | 4 |
| `jimeng-image-5.0-pro` | 2K | 8 |

The machine-readable version is `benefitCountByResolution` in models.ts (lines 68, 79, 90, 101, 112, 123, 134, 145, 156, 167), e.g. `{ "2k": 8 }` for 5.0-pro, `{ "2k": 3, "4k": 1 }` for 5.0-lite, `{ "2k": 4, "4k": 1 }` for the 4.x family, `{ "1k": 1 }` for 3.x/2.0-pro. (The `4k: 1` entries read like a *benefit* count, i.e. how many generations one benefit unit covers, not credits — treat the `API.md` table as the credit source of truth.)

### 3.3 Credit-driven auto-downgrade

`generateImagesWithRetry` (images.ts:657-724): on `EX.API_IMAGE_GENERATION_INSUFFICIENT_POINTS` **or** a message containing `积分不足` / `2039` / `1006`, it steps down the resolution list (e.g. `4k → 2k → 1k`) and retries; if the lowest tier also fails it throws with the text "积分不足，已自动降至最低画质仍然不足…".

`generateVideoWithRetry` (videos.ts:700-788): downgrades **duration first**, then **resolution** (and resets duration), walking `getVideoDurationFallbacks` → `getVideoResolutionFallbacks` (priority `4k > 1080p > 720p > 480p`).

---

## 4. Reference-image upload (shared by all image/blend/video paths)

`uploadFile(refreshToken, fileUrl, isVideoImage = false)` — core.ts:280-460. Accepts a `data:` base64 URI, an `http(s)` URL, or a **server-side local path**.

1. **Normalize to a Buffer + filename.** base64 → decoded; URL → `HEAD` pre-check (`checkFileUrl`, core.ts:250-270, rejects ≥400 and >100 MB) then `GET`; local path → `fs.readFileSync` (404-style error if missing).
2. **Get an upload token** (core.ts:327-345) — scene is retried for compatibility:
   ```js
   const uploadScenes = isVideoImage ? ["video_cover", 2] : [2];
   POST /mweb/v1/get_upload_token?aid=513695&da_version=3.2.2&aigc_features=app_lip_sync
   body: { scene }
   → { access_key_id, secret_access_key, session_token }
   ```
   Missing `access_key_id` → `'获取上传凭证失败，账号可能已掉线'`.
3. **CRC32** of the file, unsigned hex: `(crc32Value >>> 0).toString(16)`.
4. **ApplyImageUpload** — `GET https://imagex.bytedanceapi.com/?Action=ApplyImageUpload&FileSize=<n>&ServiceId=tb4s082cfz&Version=2018-08-01&s=<11 random alnum>` signed with **AWS SigV4** (region `cn-north-1`, service `imagex`, header `X-Amz-Security-Token`, optional `X-Amz-Content-Sha256`) — see `generateAWSAuthorizationHeader`, core.ts:465-542. → `Result.UploadAddress.{UploadHosts,StoreInfos,SessionKey}`.
5. **Upload bytes**: `POST https://<UploadHosts[0]>/upload/v1/<StoreInfos[0].StoreUri>` with headers `Authorization: <StoreInfos[0].Auth>`, `Content-Crc32: <crc32hex>`, `Content-Type: application/octet-stream`. Success is `data.code === 2000`.
6. **CommitImageUpload** — signed `POST` with body `{ SessionKey }` → `Result.Results[0].Uri` = the `image_uri`.

The returned `image_uri` is what goes into `ability_list[].image_uri_list` / `image_list[].image_uri` (image blend) or `first_frame_image`/`end_frame_image` (video).

---

## 5. Mode (a) — Text-to-image (文生图)

Triggered when **no** reference image paths were supplied → `hasReferenceImages === false` (images.ts:206-211, 468). Component `generate_type: "generate"`.

### 5.1 Endpoint

```
POST https://jimeng.jianying.com/mweb/v1/aigc_draft/generate
       ?aid=513695&device_platform=web&region=CN&webId=<float>
       &da_version=3.3.20&web_component_open_flag=1&web_version=7.5.0
```

Params assembled at images.ts:483-495 with `DRAFT_VERSION = "3.3.20"` (images.ts:12), `WEB_VERSION = "7.5.0"` (images.ts:18).

### 5.2 Literal request body (wire form)

Example: `model=jimeng-image-5.0-lite` → `high_aes_general_v50`, `prompt="一只在太空飞行的柴犬，赛博朋克风格"`, `ratio=1:1` → `image_ratio 8` → 2048×2048, `resolution=2k`, `n=1`, `sample_strength=0.5`, `negative_prompt=""`.

```json
{
  "extend": { "root_model": "high_aes_general_v50" },
  "submit_id": "0f2d6a3e-6c1b-4d1e-9d5a-8f7c2b1a4e33",
  "metrics_extra": "{\"promptSource\":\"custom\",\"generateCount\":1,\"enterFrom\":\"click\",\"sceneOptions\":\"[{\\\"type\\\":\\\"image\\\",\\\"scene\\\":\\\"ImageBasicGenerate\\\",\\\"modelReqKey\\\":\\\"high_aes_general_v50\\\",\\\"resolutionType\\\":\\\"2k\\\",\\\"abilityList\\\":[],\\\"benefitCount\\\":1,\\\"reportParams\\\":{\\\"enterSource\\\":\\\"generate\\\",\\\"vipSource\\\":\\\"generate\\\",\\\"extraVipFunctionKey\\\":\\\"high_aes_general_v50-2k\\\",\\\"useVipFunctionDetailsReporterHoc\\\":true}}]\",\"isBoxSelect\":false,\"isCutout\":false,\"generateId\":\"0f2d6a3e-6c1b-4d1e-9d5a-8f7c2b1a4e33\",\"isRegenerate\":false}",
  "draft_content": "{\"type\":\"draft\",\"id\":\"1c9f...\",\"min_version\":\"3.0.2\",\"min_features\":[],\"is_from_tsn\":true,\"version\":\"3.0.2\",\"main_component_id\":\"7a1b...\",\"component_list\":[{\"type\":\"image_base_component\",\"id\":\"7a1b...\",\"min_version\":\"3.0.2\",\"metadata\":{\"type\":\"\",\"id\":\"33ee...\",\"created_platform\":3,\"created_platform_version\":\"\",\"created_time_in_ms\":\"1767225600000\",\"created_did\":\"\"},\"generate_type\":\"generate\",\"aigc_mode\":\"workbench\",\"gen_type\":1,\"abilities\":{\"type\":\"\",\"id\":\"a1b2...\",\"generate\":{\"type\":\"\",\"id\":\"c3d4...\",\"core_param\":{\"type\":\"\",\"id\":\"e5f6...\",\"model\":\"high_aes_general_v50\",\"prompt\":\"一只在太空飞行的柴犬，赛博朋克风格\",\"negative_prompt\":\"\",\"seed\":2567891234,\"sample_strength\":0.5,\"image_ratio\":8,\"large_image_info\":{\"type\":\"\",\"id\":\"09ab...\",\"height\":2048,\"width\":2048,\"resolution_type\":\"2k\"},\"generate_type\":0},\"history_option\":{\"type\":\"\",\"id\":\"cd12...\"}},\"gen_option\":{\"type\":\"\",\"id\":\"ef34...\",\"gen_count\":1,\"generate_all\":false}}}]}",
  "http_common_info": { "aid": 513695 }
}
```

### 5.3 Decoded `metrics_extra` (images.ts:420-446)

```json
{
  "promptSource": "custom",
  "generateCount": 1,
  "enterFrom": "click",
  "sceneOptions": "[{\"type\":\"image\",\"scene\":\"ImageBasicGenerate\",\"modelReqKey\":\"high_aes_general_v50\",\"resolutionType\":\"2k\",\"abilityList\":[],\"benefitCount\":1,\"reportParams\":{\"enterSource\":\"generate\",\"vipSource\":\"generate\",\"extraVipFunctionKey\":\"high_aes_general_v50-2k\",\"useVipFunctionDetailsReporterHoc\":true}}]",
  "isBoxSelect": false,
  "isCutout": false,
  "generateId": "<same as submit_id>",
  "isRegenerate": false
}
```

* `sceneOptions` is a **JSON string nested inside a JSON string** — double-encoded telemetry. `scene` is the literal `"ImageBasicGenerate"`.
* `extraVipFunctionKey` = `${model}-${resolutionType}` — this is what the backend uses to decide the VIP/benefit channel.
* `benefitCount` = `n`; `generateCount` = `n`.
* **`metrics_extra` is omitted entirely in blend mode** (it is set to `undefined`, images.ts:420-421, and `JSON.stringify` drops undefined keys).

### 5.4 Decoded `draft_content` — text-to-image

```json
{
  "type": "draft",
  "id": "<uuid>",
  "min_version": "3.0.2",
  "min_features": [],
  "is_from_tsn": true,
  "version": "3.0.2",
  "main_component_id": "<componentId>",
  "component_list": [{
    "type": "image_base_component",
    "id": "<componentId>",
    "min_version": "3.0.2",
    "metadata": {
      "type": "", "id": "<uuid>",
      "created_platform": 3,
      "created_platform_version": "",
      "created_time_in_ms": "1767225600000",
      "created_did": ""
    },
    "generate_type": "generate",
    "aigc_mode": "workbench",
    "gen_type": 1,
    "abilities": {
      "type": "", "id": "<uuid>",
      "generate": {
        "type": "", "id": "<uuid>",
        "core_param": {
          "type": "", "id": "<uuid>",
          "model": "high_aes_general_v50",
          "prompt": "<prompt>",
          "negative_prompt": "",
          "seed": 2567891234,
          "sample_strength": 0.5,
          "image_ratio": 8,
          "large_image_info": { "type": "", "id": "<uuid>", "height": 2048, "width": 2048, "resolution_type": "2k" },
          "generate_type": 0
        },
        "history_option": { "type": "", "id": "<uuid>" }
      },
      "gen_option": { "type": "", "id": "<uuid>", "gen_count": 1, "generate_all": false }
    }
  }]
}
```

Field notes:

* `main_component_id` **must equal** the component's own `id` (both from the same `componentId = util.uuid()`, images.ts:298).
* `created_time_in_ms` is a **string** here (images.ts:465) — unlike the video path, which passes a number.
* `seed = Math.floor(Math.random() * 100000000) + 2500000000` (images.ts:386) — i.e. in `[2.5e9, 2.6e9)`.
* `gen_type: 1` is the workbench image type; `generate_type: 0` inside `core_param` is the "plain generate" sub-type.
* `sample_strength` = "精细度", default `0.5` (images.ts:190).
* `negative_prompt` is **ignored** in blend mode (not emitted at all).
* **Version asymmetry (images.ts:12-19):** the *request* `da_version` is `3.3.20` (=`DRAFT_VERSION`), but the *draft payload* uses `version = DRAFT_CONTENT_VERSION = "3.0.2"` and `min_version = MIN_VERSION = "3.0.2"`. The comment at images.ts:13-17 says the payload is kept field-for-field aligned with the web client and that `abilities.gen_option` **must be on the `abilities` object**, not on the component — Jimeng ignores it there.
* `n` (`gen_option.gen_count`, max 8 per routes/images.ts:52) is the batch size; the credit cost multiplies by it.

---

## 6. Mode (b) — Image-to-image / reference image (图生图 / 混合模式)

Triggered when ≥1 reference path is supplied (`uploadIDs.length > 0`, images.ts:303). Up to **10** images (routes/images.ts:11, 81-86). Each is uploaded and its `image_uri` collected in order (images.ts:215-233).

Differences from mode (a):

| Aspect | value |
|---|---|
| component `generate_type` | **`"blend"`** (images.ts:468) |
| abilities branch | `blend` (not `generate`) — images.ts:308 |
| prompt | `prompt + "##"` — a literal `##` suffix is appended (images.ts:318) |
| `negative_prompt`, `seed`, `core_param.generate_type` | **absent** |
| `metrics_extra` | **absent** (undefined) |
| extra blocks | `min_features: []`, `ability_list`, `history_option`, `prompt_placeholder_info_list`, `postedit_param` |
| model | **not** forced to 3.0 — the user-selected model is used (comment at images.ts:235) |

### 6.1 Decoded `draft_content` — image-to-image

```json
{
  "type": "draft", "id": "<uuid>", "min_version": "3.0.2", "min_features": [],
  "is_from_tsn": true, "version": "3.0.2",
  "main_component_id": "<componentId>",
  "component_list": [{
    "type": "image_base_component", "id": "<componentId>", "min_version": "3.0.2",
    "metadata": { "type": "", "id": "<uuid>", "created_platform": 3,
                  "created_platform_version": "", "created_time_in_ms": "1767225600000",
                  "created_did": "" },
    "generate_type": "blend",
    "aigc_mode": "workbench",
    "gen_type": 1,
    "abilities": {
      "type": "", "id": "<uuid>",
      "blend": {
        "type": "", "id": "<uuid>",
        "min_features": [],
        "core_param": {
          "type": "", "id": "<uuid>",
          "model": "high_aes_general_v43",
          "prompt": "把参考图变成梵高风格##",
          "sample_strength": 0.5,
          "image_ratio": 8,
          "large_image_info": { "type": "", "id": "<uuid>", "height": 2048, "width": 2048, "resolution_type": "2k" }
        },
        "ability_list": [{
          "type": "", "id": "<uuid>",
          "name": "byte_edit",
          "image_uri_list": ["<image_uri_1>", "<image_uri_2>"],
          "image_list": [{
            "type": "image", "id": "<uuid>", "source_from": "upload", "platform_type": 1,
            "name": "", "image_uri": "<image_uri_1>", "width": 0, "height": 0,
            "format": "", "uri": "<image_uri_1>"
          }],
          "strength": 0.5
        }],
        "history_option": { "type": "", "id": "<uuid>" },
        "prompt_placeholder_info_list": [{ "type": "", "id": "<uuid>", "ability_index": 0 }],
        "postedit_param": { "type": "", "id": "<uuid>", "generate_type": 0 }
      },
      "gen_option": { "type": "", "id": "<uuid>", "gen_count": 1, "generate_all": false }
    }
  }]
}
```

Notes:

* The ability name is the literal **`"byte_edit"`** (images.ts:330) and `strength` is hard-coded **`0.5`** (images.ts:345) — *not* the user's `sample_strength`.
* `image_uri_list` and `image_list` carry the **same** uris; `width`/`height`/`format` are all empty because the real metadata lives server-side.
* `prompt_placeholder_info_list[0].ability_index = 0` binds the first ability to the prompt placeholder.
* `postedit_param.generate_type = 0`.
* The outer body keeps `extend: { root_model: <model> }`, `submit_id`, `http_common_info.aid`.

---

## 7. Mode (c) — Text-to-video (文生视频)

Triggered when no frame images are uploaded → `hasReferenceFrame === false` → `video_mode: 2` (videos.ts:577).

### 7.1 Endpoint + params (videos.ts:511-521)

```
POST https://jimeng.jianying.com/mweb/v1/aigc_draft/generate
       ?aid=513695&device_platform=web&region=CN&webId=<float>
       &aigc_features=app_lip_sync&web_version=7.5.0&da_version=3.3.20&web_component_open_flag=1
```

Note the video path adds `aigc_features=app_lip_sync`.

### 7.2 Literal request body (wire form)

Example: `model=jimeng-video-seedance-2.0` → `dreamina_seedance_40_pro`, `resolution=720p`, `ratio=16:9`, `duration=5` → `duration_ms=5000`.

```json
{
  "extend": {
    "root_model": "dreamina_seedance_40_pro",
    "m_video_commerce_info": {
      "benefit_type": "dreamina_video_seedance_20_pro",
      "resource_id": "generate_video",
      "resource_id_type": "str",
      "resource_sub_type": "aigc"
    },
    "m_video_commerce_info_list": [{
      "benefit_type": "dreamina_video_seedance_20_pro",
      "resource_id": "generate_video",
      "resource_id_type": "str",
      "resource_sub_type": "aigc"
    }]
  },
  "submit_id": "8b1d0c44-2f5a-4b6e-9a01-2c3d4e5f6a7b",
  "metrics_extra": "{\"enterFrom\":\"click\",\"isDefaultSeed\":1,\"promptSource\":\"custom\",\"isRegenerate\":false,\"originSubmitId\":\"5e6f7a8b-9c0d-4e1f-8a2b-3c4d5e6f7a8b\"}",
  "draft_content": "{\"type\":\"draft\",\"id\":\"...\",\"min_version\":\"3.0.5\",\"is_from_tsn\":true,\"version\":\"3.3.20\",\"main_component_id\":\"<componentId>\",\"component_list\":[{\"type\":\"video_base_component\",\"id\":\"<componentId>\",\"min_version\":\"1.0.0\",\"metadata\":{\"type\":\"\",\"id\":\"...\",\"created_platform\":3,\"created_platform_version\":\"\",\"created_time_in_ms\":1767225600000,\"created_did\":\"\"},\"generate_type\":\"gen_video\",\"aigc_mode\":\"workbench\",\"abilities\":{\"type\":\"\",\"id\":\"...\",\"gen_video\":{\"id\":\"...\",\"type\":\"\",\"text_to_video_params\":{\"type\":\"\",\"id\":\"...\",\"model_req_key\":\"dreamina_seedance_40_pro\",\"priority\":0,\"seed\":2567891234,\"video_aspect_ratio\":\"16:9\",\"video_gen_inputs\":[{\"duration_ms\":5000,\"first_frame_image\":null,\"end_frame_image\":null,\"fps\":24,\"id\":\"...\",\"min_version\":\"3.0.5\",\"prompt\":\"女孩在沙滩奔跑，夕阳逆光\",\"resolution\":\"720p\",\"type\":\"\",\"video_mode\":2}]},\"video_task_extra\":\"{\\\"enterFrom\\\":\\\"click\\\",\\\"isDefaultSeed\\\":1,\\\"promptSource\\\":\\\"custom\\\",\\\"isRegenerate\\\":false,\\\"originSubmitId\\\":\\\"5e6f7a8b-9c0d-4e1f-8a2b-3c4d5e6f7a8b\\\"}\"}}}]}",
  "http_common_info": { "aid": 513695 }
}
```

### 7.3 Decoded `draft_content` — video

```json
{
  "type": "draft",
  "id": "<uuid>",
  "min_version": "3.0.5",
  "is_from_tsn": true,
  "version": "3.3.20",
  "main_component_id": "<componentId>",
  "component_list": [{
    "type": "video_base_component",
    "id": "<componentId>",
    "min_version": "1.0.0",
    "metadata": {
      "type": "", "id": "<uuid>", "created_platform": 3,
      "created_platform_version": "",
      "created_time_in_ms": 1767225600000,
      "created_did": ""
    },
    "generate_type": "gen_video",
    "aigc_mode": "workbench",
    "abilities": {
      "type": "", "id": "<uuid>",
      "gen_video": {
        "id": "<uuid>", "type": "",
        "text_to_video_params": {
          "type": "", "id": "<uuid>",
          "model_req_key": "dreamina_seedance_40_pro",
          "priority": 0,
          "seed": 2567891234,
          "video_aspect_ratio": "16:9",
          "video_gen_inputs": [{
            "duration_ms": 5000,
            "first_frame_image": null,
            "end_frame_image": null,
            "fps": 24,
            "id": "<uuid>",
            "min_version": "3.0.5",
            "prompt": "女孩在沙滩奔跑，夕阳逆光",
            "resolution": "720p",
            "type": "",
            "video_mode": 2
          }]
        },
        "video_task_extra": "{\"enterFrom\":\"click\",\"isDefaultSeed\":1,\"promptSource\":\"custom\",\"isRegenerate\":false,\"originSubmitId\":\"<uuid>\"}"
      }
    }
  }]
}
```

Field notes:

* `video_base_component` / `gen_video` / `text_to_video_params` / `video_gen_inputs` is the whole video ability tree — it is **not** `generate`/`blend` like images.
* The model key field is named **`model_req_key`** here (vs `model` for images).
* `created_time_in_ms` is a **number** (`Date.now()`, videos.ts:547) — images use a string.
* Draft versions differ from images: `min_version "3.0.5"`, outer `version "3.3.20"`, component `min_version "1.0.0"`, input `min_version "3.0.5"` (videos.ts:530-548). There's no `min_features`/`gen_type` on the video component.
* `seed` uses the same `[2.5e9, 2.6e9)` formula (videos.ts:563).
* `fps` is fixed at **24**; `priority: 0`; `type: ""` everywhere (the API's polymorphic-discriminator trick).
* `video_task_extra` is the same JSON **string** as `metrics_extra` (videos.ts:581, built at 494-500). `metrics_extra` for video is a *different* shape than for images: `{enterFrom, isDefaultSeed, promptSource, isRegenerate, originSubmitId}` — no `sceneOptions`.
* `extend.m_video_commerce_info` **and** `m_video_commerce_info_list` both carry the identical object; built by `getVideoCommerceInfoFromConfig` (videos.ts:175-186):
  `benefit_type` = `benefits[resolution] || defaultBenefit || "basic_video_operation_vgfm_v_three"`, plus `resource_id: "generate_video"`, `resource_id_type: "str"`, `resource_sub_type: "aigc"`. The full `benefit_type` table is §1.2 / videos.ts:53-145 (e.g. `seedance_20_pro_720p_output`, `seedance_20_pro_1080p_output`, `seedance_20_pro_4k_output`, `dreamina_video_seedance_20_pro`, `basic_video_operation_vgfm_v_three_pro`).

---

## 8. Mode (d) — Image-to-video (图生视频 / 首尾帧)

videos.ts:427-491 and 505-577.

* `filePaths[0]` → `first_frame_image`, `filePaths[1]` → `end_frame_image`; further paths are uploaded but **ignored**. Upload uses `uploadFile(token, path, true)` → `isVideoImage = true` → upload scene list `["video_cover", 2]` (core.ts:327-329). A failure on index 0 throws `'首帧上传失败'`; later failures only log.
* Frame object shape (videos.ts:464-489) — note `source_from: "upload"`, `platform_type: 1`, and **zeroed** dimensions:
  ```json
  { "format": "", "height": 0, "id": "<uuid>", "image_uri": "<uploaded uri>",
    "name": "", "platform_type": 1, "source_from": "upload", "type": "image",
    "uri": "<uploaded uri>", "width": 0 }
  ```
* The decisive switch is `video_mode` (videos.ts:576-577):
  ```js
  // 即梦网页端：0 是带首/尾帧的 V1 图生视频，2 是默认文生视频。
  video_mode: hasReferenceFrame ? 0 : 2,
  ```
  i.e. **`video_mode: 0`** whenever either frame exists, **`2`** for pure text-to-video. There is no separate image-to-video endpoint or component type — it is the same `gen_video` → `text_to_video_params` block with `first_frame_image`/`end_frame_image` populated and `video_mode` flipped.
* Body is otherwise identical to mode (c). Both `first_frame_image` and `end_frame_image` serialize as `null` in text-to-video (they are `undefined` → JSON.stringify emits `null` inside the stringified `draft_content`... note: inside `JSON.stringify` nested undefined **object values** become `null`, which is what Jimeng expects).

---

## 9. Response → draft/history id

Both paths read from the unwrapped `data` (because `checkResult` returns `data` for `ret === "0"`):

```js
const { aigc_data } = await request("post", "/mweb/v1/aigc_draft/generate", refreshToken, {...});
const historyId = aigc_data.history_record_id;         // images.ts:496-498, videos.ts:594-596
if (!historyId) throw new APIException(..., "记录ID不存在");
```

So the wire response is `{ ret: "0", errmsg: "", data: { aigc_data: { history_record_id: "<id>", ... } } }` and the id you keep is **`data.aigc_data.history_record_id`**. (The draft `id`/`submit_id` from the request are *not* used for polling.)

---

## 10. Polling flow

### 10.1 Shared status semantics

```js
const PROCESSING_STATES = [20, 42, 45];   // images.ts:507   (20=queued, 42/45 = jimeng-4.5 in-progress)
const FAIL_STATE = 30;                    // images.ts:508
const VIDEO_PROCESSING_STATES = [20, 42, 45];  // videos.ts:243
```

The comment at images.ts:500-506 is the authoritative legend:

```
20 = 初始提交/队列中
42 = 处理中（jimeng-4.5 新状态）
45 = 处理中（jimeng-4.5 中间状态）
50 = 完成/有结果（jimeng-4.5）
21 = 生成成功（旧版本）
30 = 生成失败
```

Practical success test used by the code: **`status` not in `{20,42,45}` AND `item_list.length > 0`** (or, for video, a URL extractable from `item_list`). So 21 and 50 are "done".

### 10.2 Image polling — `POST /mweb/v1/get_history_by_ids`

Loop (images.ts:516-636): 1 s sleep between attempts, `MAX_POLL_RETRIES = 120` (~2 min wall clock), status logged every 5th attempt.

Request body:

```json
{
  "history_ids": ["<history_record_id>"],
  "image_info": {
    "width": 2048,
    "height": 2048,
    "format": "webp",
    "image_scene_list": [
      { "scene": "smart_crop", "width": 360, "height": 360, "uniq_key": "smart_crop-w:360-h:360", "format": "webp" },
      { "scene": "smart_crop", "width": 480, "height": 480, "uniq_key": "smart_crop-w:480-h:480", "format": "webp" },
      { "scene": "smart_crop", "width": 720, "height": 720, "uniq_key": "smart_crop-w:720-h:720", "format": "webp" },
      { "scene": "smart_crop", "width": 720, "height": 480, "uniq_key": "smart_crop-w:720-h:480", "format": "webp" },
      { "scene": "smart_crop", "width": 360, "height": 240, "uniq_key": "smart_crop-w:360-h:240", "format": "webp" },
      { "scene": "smart_crop", "width": 240, "height": 320, "uniq_key": "smart_crop-w:240-h:320", "format": "webp" },
      { "scene": "smart_crop", "width": 480, "height": 640, "uniq_key": "smart_crop-w:480-h:640", "format": "webp" },
      { "scene": "normal", "width": 2400, "height": 2400, "uniq_key": "2400", "format": "webp" },
      { "scene": "normal", "width": 1080, "height": 1080, "uniq_key": "1080", "format": "webp" },
      { "scene": "normal", "width": 720, "height": 720, "uniq_key": "720", "format": "webp" },
      { "scene": "normal", "width": 480, "height": 480, "uniq_key": "480", "format": "webp" },
      { "scene": "normal", "width": 360, "height": 360, "uniq_key": "360", "format": "webp" }
    ]
  },
  "http_common_info": { "aid": 513695 }
}
```

(The `image_info` block just asks the CDN for pre-rendered variants; it is not the source of the final URL.)

Response handling (images.ts:627-650):

```js
if (!result[historyId]) throw new APIException(EX.API_IMAGE_GENERATION_FAILED, "记录不存在");
status   = result[historyId].status;
failCode = result[historyId].fail_code;
item_list = result[historyId].item_list;
...
if (status === FAIL_STATE) {
  if (failCode === "2038") throw new APIException(EX.API_CONTENT_FILTERED);
  else throw new APIException(EX.API_IMAGE_GENERATION_FAILED);
}
return item_list.map((item) => {
  if (!item?.image?.large_images?.[0]?.image_url)
    return item?.common_attr?.cover_url || null;
  return item.image.large_images[0].image_url;
}).slice(0, n);
```

* Response shape: a **map keyed by history id** — `{ "<history_id>": { status, fail_code, item_list: [...] } }` (no `ret`/`data` envelope, hence the `isFinite(Number(ret))` guard in `checkResult`).
* Final image URL: `item.image.large_images[0].image_url`, fallback `item.common_attr.cover_url`, else `null` for that slot.
* `fail_code === "2038"` = content filtered (`EX.API_CONTENT_FILTERED`).
* Timeout (`retryCount >= 120`) → `"图像生成超时"`.

### 10.3 Video polling — `get_history_by_ids` ⇄ `get_history_records`

Loop (videos.ts:599-693): a fixed **5 s warm-up sleep first** (videos.ts:605), then `maxRetries = 60` iterations with backoff `2000 * min(retryCount + 1, 5)` ms (so 4 s…10 s), i.e. "20 mins" per the comment on line 603.

Two endpoints are alternated (videos.ts:616-631):

```js
let useAlternativeApi = retryCount > 10 && retryCount % 2 === 0;
if (useAlternativeApi) {
  result = await request("post", "/mweb/v1/get_history_records", refreshToken, {
    data: { history_record_ids: [historyId] },
  });
} else {
  result = await request("post", "/mweb/v1/get_history_by_ids", refreshToken, {
    data: { history_ids: [historyId] },
  });
}
```

Note the video path sends the **minimal** body `{ history_ids: [...] }` — no `image_info`, no `http_common_info`.

URL extraction (videos.ts:245-268) — this is the union of every shape observed:

```js
function extractVideoUrlFromItemList(itemList = []) {
  for (const item of itemList) {
    const url =
      item?.video?.transcoded_video?.origin?.video_url ||
      item?.video?.play_url ||
      item?.video?.download_url ||
      item?.video?.url;
    if (url) return url;
  }
}

function extractVideoUrlFromResponse(result) {
  const historyRecords = [...(result?.history_list || []), ...(result?.history_records || [])];
  for (const record of historyRecords) {
    const url = extractVideoUrlFromItemList(record?.item_list || []);
    if (url) return url;
  }
  // last-resort: regex the whole payload for a vod URL
  const responseStr = JSON.stringify(result);
  return responseStr.match(/https:\/\/[^"\s]+(?:vlabvod|vod)[^"\s]+/)?.[0];
}
```

Status handling (videos.ts:633-679):

* If a URL is found → return immediately (URL presence short-circuits the status check).
* History record: `result.history_records[0]` when using the alternative API, else `result.history_list[0]`; fields `status`, `fail_code`, `item_list`.
* `status === 30` → throws `生成失败: <fail_code>`.
* Still processing → increment retry, back off, loop. Network errors are swallowed and retried with a flat 5 s delay; `APIException` propagates.
* Exhausted retries → `"超时"`; if the loop exits but no URL was found → `视频生成结束但未返回可用URL，状态码: <status>[, 失败码: <fail_code>]`.

---

## 11. Public HTTP surface (for context)

`routes/images.ts` `POST /v1/images/generations` — OpenAI Images-ish:

| field | type | default | notes |
|---|---|---|---|
| `model` | string | `jimeng-image-5.0-lite` | unknown → default |
| `prompt` | string | required | |
| `negative_prompt` | string | `""` | ignored in blend mode |
| `ratio` | string | `1:1` | any of the 8 ratios |
| `resolution` | string | model default | `4k`/`2k`/`1.5k`/`1k` |
| `sample_strength` | number | `0.5` | |
| `n` | int 1-8 | `1` | → `gen_option.gen_count` |
| `response_format` | string | `url` | `b64_json` fetches + base64s the URL |
| `filePath` / `filePaths` | string / string[] | — | local path, http(s) URL, or `data:` base64 |
| multipart files | any field name | — | all collected, total ≤ 10 |

Response: `{ created, data: [{ url } | { b64_json }] }`.

`routes/videos.ts` `POST /v1/videos/generations`: `model` (default `jimeng-video-seedance-2.0`), `prompt`, `ratio`, `resolution`, `duration` (int), `file_paths[]`, `response_format`. Response: `{ created, data: [{ url, revised_prompt }] }`.

Error codes (API.md §8 / `src/lib/consts/exceptions.ts`): `-2000` invalid params, `-2001` request failed (login expired / gateway), `-2002` token expired, `-2003` remote file URL invalid, `-2004` remote file too large, `-2005` stream already running, `-2006` content compliance block, `-2007` image generation failed, `-2008` video generation failed, `-2009` insufficient credits, `-9999` unknown. Shape: `{ "code": -2009, "message": "...", "data": null }`.

`getTokenLiveStatus` (core.ts:579-596): `POST /passport/account/info/v2?account_sdk_source=web` → `data.user_id` truthy = live.

---

## 12. Reimplementation checklist / gotchas

1. **Double-encoded JSON.** `draft_content`, `metrics_extra` (images + videos) and `video_task_extra` are JSON **strings**; `sceneOptions` inside `metrics_extra` is a string-inside-a-string.
2. **Every uuid is generated client-side** via `uuid v4`; `main_component_id` must repeat the component id; `metrics_extra.generateId` must repeat `submit_id`.
3. **Version triple:** request `da_version=3.3.20` (both modes) + `web_version=7.5.0`; image draft `version=3.0.2`/`min_version=3.0.2`; video draft `version=3.3.20`/`min_version=3.0.5`/component `min_version=1.0.0`.
4. **Sign covers only the last 7 chars of the path** — a path change silently changes the signature input, not the whole path.
5. **`gen_option` must sit on `abilities`**, not on the component (images.ts:15-16).
6. **`video_mode` is the only image-to-video switch** (0 = frames, 2 = text).
7. Blend mode has **no seed, no negative_prompt, no metrics_extra**, and appends `"##"` to the prompt.
8. `n` > 1 returns up to `n` URLs from `item_list` (`.slice(0, n)`), and `benefitCount`/`generateCount` should track `n`.
9. Credits are checked (and the daily grant claimed) **before every submission**; `ret === "5000" | "1006"` = insufficient, `status === 30 && fail_code === "2038"` = filtered content.
10. Polling needs **both** the status transition **and** a non-empty `item_list`; the video path additionally falls back to a regex over the whole response for a `vlabvod`/`vod` URL.
11. Uploaded reference images must go through the ImageX three-step dance (ApplyImageUpload → PUT → CommitImageUpload); the committed `Uri` (not the StoreUri) is the `image_uri`.
