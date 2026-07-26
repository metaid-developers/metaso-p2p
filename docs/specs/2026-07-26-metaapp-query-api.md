# MetaAPP 聚合查询 API 需求

## 背景

AI Agent 浏览器（宿主 IDBots）需要在左侧 AI 对话条中支持"用自然语言找链上 MetaAPP"：用户说"帮我找最近七天的小游戏""打开某个 MetaID 最新的 MetaAPP""展示基于某个应用派生出的其他应用"，宿主 LLM 把意图翻译成结构化检索参数，调用聚合 API 拿到 5~10 个候选，再由 LLM 从中选择一个以 `metaapp://<pinId>` 打开。

metaso-p2p 的 `publishedcontent` 聚合器已经对 `/protocols/metaapp` 完成实时索引（四链扫链 + MAN 历史回填 + create/modify/revoke 版本折叠 + 发布者身份索引），payload 完整存于 `Record.PayloadJSON`。本需求是在其上补齐公开查询 API（即 publishedcontent 规划中的 "Task 7 wires public router exposure"）。索引层零改动，不新增任何配置项。

接口风格遵循 metaso-p2p 主体约定：`{code, data, message, processingTime}` 外层包裹，成功 `code=0`，业务错误码只用 `40000/40400/50000`，HTTP 恒 200。列表分页使用 opaque `nextCursor`。

## 总体原则

- 聚合端只做声明式数据聚合：索引、折叠、字段归一、过滤、排序。不做"这个应用好不好/安不安全"之类的主观业务裁决。
- 语义理解发生在宿主 LLM 层：聚合端提供关键词/标签/时间/作者/派生关系的结构化检索，"哪个应用最符合意图"由宿主 LLM 从候选中选择。
- 默认列表只返回 latest、非 revoke、`disabled != true` 的应用；这是对链上声明状态的筛选。
- 检索语料为 `title/appName/intro/tags`，**不含 `prompt`**（AI 生成提示词太长且噪声大，仅在 detail 返回）。
- 向量/语义检索 v1 不做。若未来应用量级增长导致关键词召回不足，再扩展（方向：embedding 走可配置外部 HTTP 服务、向量存 Pebble namespace、内存余弦，做成可选模块，不引入重外部依赖）。

## API 1: MetaAPP 列表 / 检索

### Endpoint

`GET /api/metaapp/list`

### 用途

MetaAPP 的全局 feed 与意图检索。无过滤条件时即"最新应用"列表；配合参数覆盖："最近 N 天的 X 类应用"（keyword+since）、"某 MetaID/地址发布的应用"（publisher）、"支持某协议的应用"（tag）等场景。

### Query Parameters

| 参数 | 类型 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- | --- |
| `keyword` | string | 否 | - | 空格分词，AND 语义，大小写不敏感子串匹配；语料 `title/appName/intro/tags` |
| `tag` | string | 否 | - | 逗号分隔多个标签，任一命中；对 payload `tags` 精确匹配（大小写不敏感） |
| `chainName` | string | 否 | - | 按应用所在链筛选，如 `mvc`、`btc`、`doge`、`opcat` |
| `runtime` | string | 否 | - | contains 匹配（大小写不敏感），`browser` 可命中 `browser/android` |
| `publisher` | string | 否 | - | 对发布者 `globalMetaId/metaId/address` 三字段任一命中（大小写不敏感）；配 `size=1` 即"该用户最新应用" |
| `since` | number | 否 | - | unix 秒，只返回 `updatedAt >= since` 的应用 |
| `until` | number | 否 | - | unix 秒，只返回 `updatedAt <= until` 的应用 |
| `includeDisabled` | number | 否 | `0` | `1` 时包含 payload 声明 `disabled=true` 的应用（revoke 的恒不返回） |
| `size` | number | 否 | `20` | 每页数量，上限 100 |
| `cursor` | string | 否 | - | opaque 游标，非法游标返回 40000 |

### 排序

- 有 `keyword`：先按相关性分降序（tag 命中 ×3 + title/appName 命中 ×2 + intro 命中 ×1，按命中的分词数累计），再按 `updatedAt` 降序。
- 无 `keyword`：按 `updatedAt` 降序。
- 并列兜底：`chainName`、`pinId` 字典序。

### Response

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "items": [
      {
        "pinId": "source-pin-id",
        "sourcePinId": "source-pin-id",
        "currentPinId": "current-pin-id",
        "chainName": "mvc",
        "title": "番茄钟",
        "appName": "pomodoro",
        "intro": "极简风格的番茄钟工具",
        "tags": ["tool", "timer"],
        "icon": "metafile://<pinId>.png",
        "coverImg": "metafile://<pinId>.jpg",
        "runtime": "browser",
        "version": "1.0.0",
        "content": "metafile://<pinId>.zip",
        "indexFile": "index.html",
        "forkedFrom": "",
        "disabled": false,
        "publisherGlobalMetaId": "...",
        "publisherMetaId": "...",
        "publisherAddress": "...",
        "publisherName": "Alice",
        "publisherAvatarId": "<avatar-pin-id>i0",
        "createdAt": 1768284841,
        "updatedAt": 1768284841
      }
    ],
    "nextCursor": "eyJvIjoyMH0",
    "hasMore": true
  }
}
```

- `pinId` 为版本链的**稳定根 pin**（source pin，与 `sourcePinId` 相同）——MetaID 的 modify/revoke 都锚定在原始 pin 上，宿主构造打开地址应使用 `metaapp://<pinId>`（即原始 pin）。该语义与 Bot Homepage v3 section item 的 `pinId` 规则一致。`currentPinId` 为版本链最新 pin，可用于判断应用是否有更新；从未修改的应用三者相同。
- 列表项的 title/intro/tags 等 payload 字段取自版本链**最新**记录，与稳定 pinId 组合返回。
- `icon/coverImg/content` 原样返回 `metafile://` URI，由调用方按既有 metafile 链路自行解析下载。
- `publisherName` / `publisherAvatarId` 来自 userinfo 聚合的发布者资料补全（头像 pinId，可经 metafile 链路取内容）；发布者无资料时两个字段缺省。
- `createdAt/updatedAt` 为 unix 秒。

## API 2: MetaAPP 详情

### Endpoint

`GET /api/metaapp/detail/:pinId`

### 用途

打开应用前获取完整 manifest。`:pinId` 接受版本链上任意版本 pinId（自动解析到最新记录）。

### Query Parameters

| 参数 | 类型 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- | --- |
| `chainName` | string | 否 | - | 提供则直查；不提供则跨链扫描解析 |

### Response

`data` 为列表项字段超集，额外包含：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "pinId": "...",
    "...": "（同列表项全部字段）",
    "prompt": "You are an AI...",
    "payload": { "（链上原始 payload JSON）" }
  }
}
```

应用不存在返回 `code=40400`。

## API 3: MetaAPP 派生列表

### Endpoint

`GET /api/metaapp/forks/:pinId`

### 用途

"展示基于某个 MetaAPP 派生出的其他 MetaAPP"：返回 payload 中 `forkedfrom`/`forkedFrom` 指向该应用版本链的直接子代。子代引用父代任意版本 pinId 均可聚簇到同一版本链。fork 的 fork 不在 v1 递归，调用方可对子代再次调用本接口。

### Query Parameters

| 参数 | 类型 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- | --- |
| `size` | number | 否 | `20` | 每页数量，上限 100 |
| `cursor` | string | 否 | - | opaque 游标 |

排序：`createdAt` 降序。父应用不存在返回 `code=40400`。响应结构同 API 1（`items/nextCursor/hasMore`）。

## MetaAppItem 字段归一规则

链上 payload 字段名存在多套写法，聚合端统一按下表归一（按优先级取第一个非空字符串）：

| 输出字段 | payload 提取键 |
| --- | --- |
| `title` | `title` → `name` → `displayName` |
| `appName` | `appName` → `appname` |
| `intro` | `intro` → `description` → `summary` |
| `tags` | `tags`（数组元素字符串化） |
| `icon` / `coverImg` / `runtime` / `version` / `content` | 同名键 |
| `indexFile` | `indexFile`，缺省 `index.html` |
| `forkedFrom` | `forkedfrom` → `forkedFrom` |
| `disabled` | `disabled`（容忍 `true` 与 `"true"`） |

## 错误码

| code | 场景 |
| --- | --- |
| 40000 | 参数非法（size/since/until/includeDisabled 无法解析、cursor 非法） |
| 40400 | detail/forks 的目标应用不存在 |
| 50000 | 聚合内部错误 |

## 协议能力声明约定（发布侧配合）

"能显示 simplebuzz 的 MetaAPP""能发布链上笔记的应用"这类能力检索，依赖发布方在 payload `tags` 中声明能力标签（建议直接使用协议名，如 `simplebuzz`、`simplenote`）。聚合端不做 payload 内容推断；未声明能力的应用只能靠 `intro` 文本关键词兜底命中。

## 明确不做（v1）

- 向量/语义检索（见"总体原则"）。
- tag/forkedFrom 二级索引（当前应用量级下，时间索引扫描 + 内存过滤足够；量级增长后再加）。
- icon/cover 等资产的 URL 改写（保持 `metafile://` 原样返回）。
- forks 递归整棵派生树。
- 新增配置项。
