# MetaID 聚合搜索 API 需求

## 背景

AI Agent 浏览器（宿主 IDBots）已对接 MetaAPP 聚合查询 API（[`2026-07-26-metaapp-query-api.md`](2026-07-26-metaapp-query-api.md)），下游 LLM 把「找最近七天的小游戏」这类意图翻译成结构化检索参数调用 `/api/metaapp/list`，从候选中选一个打开，链路验证效果良好。本需求是其对仗的「找人」版本，下游 LLM 的三类典型意图：

- 「查看某某某的 bot page」→ 按名字检索，取最佳匹配候选的 `globalMetaId` 打开 bot page；
- 「查看某某某的具体信息」→ 返回指定身份的基础资料（globalMetaId、名字、头像、bio 等），由下游 LLM 组织呈现；
- 「找性格活泼开朗的用户/bot 聊天」→ 在 persona/bio 等语料中检索，返回可私信（已设置 chatpubkey）的候选，下游打开 bot page 或发起私聊。

userinfo 聚合器已完成 `/info/*` 实时索引（name/avatar/bio/role/soul/goal/chatskills/llm/persona/homepage/background/chatpubkey，四链扫链 + MAN 历史回填 + 每路径 revision 最新者胜），身份三字段（globalMetaId/metaId/address）具备反向索引且启动时全量预热进内存。本需求在其上补齐公开搜索 API。索引层仅新增两处：profile 粒度 `updatedAt` 维护（取各 `/info` 路径 revision 时间戳的 max）与内存搜索文档注册表；不新增配置项。

接口风格与 MetaAPP 完全对齐（下游只需学一套）：`{code, data, message, processingTime}` 外层包裹，成功 `code=0`，业务错误码只用 `40000/40400/50000`，HTTP 恒 200，列表分页使用 opaque `nextCursor`。

## 总体原则

- 聚合端只做声明式数据聚合：索引、字段归一、过滤、排序。「哪个人最符合意图」由宿主 LLM 从候选中选择，聚合端不做主观裁决。
- 检索语料为 `name/bio/role/soul/goal/persona/chatSkills/llm` 的文本；不含 avatar/background 等二进制引用，不含 chatpubkey、homepage URI 等不可读字段（这些仅在 detail 返回）。
- 中文友好的子串匹配（大小写不敏感 contains），不分词、不做同义词/语义扩展；召回不足由下游 LLM 用近义分词重试兜底（同 MetaAPP 指南的空结果降级策略）。
- 注册后从未写过任何可检索 `/info` 字段的「空 profile」不进入搜索语料：它既无法被 keyword 命中，也不应出现在无参数 feed 中。
- 向量/语义检索 v1 不做。若未来用户量级增长导致关键词召回不足，再扩展（方向同 MetaAPP spec：可配置外部 embedding HTTP 服务 + 内存余弦，做成可选模块）。

## API 1: MetaID 列表 / 检索

### Endpoint

`GET /api/metaid/list`

### 用途

MetaID 用户的全局 feed 与意图检索。无过滤条件时即「最近更新的用户」列表（按 `updatedAt` 降序）；配合参数覆盖：「查看某某某」（keyword 精确名加权）、「找性格 X 的人」（keyword 命中 persona/bio）、「找会某技能的 bot」（skill）、「找能私聊的人」（hasChatPubkey）等场景。

### Query Parameters

| 参数 | 类型 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- | --- |
| `keyword` | string | 否 | - | 空格分词，AND 语义，大小写不敏感子串匹配；语料见「检索语料与打分」 |
| `skill` | string | 否 | - | 对解析后的 chatSkills 技能名 contains 匹配（大小写不敏感） |
| `chainName` | string | 否 | - | 按用户注册所在链筛选，如 `mvc`、`btc`、`doge`、`opcat` |
| `hasChatPubkey` | number | 否 | `0` | `1` 时只返回已设置 chatpubkey（可接收私信）的用户 |
| `hasHomepage` | number | 否 | `0` | `1` 时只返回声明了自定义 homepage（`/info/homepage` 非空）的用户 |
| `since` | number | 否 | - | unix 秒，只返回 `updatedAt >= since` 的用户 |
| `until` | number | 否 | - | unix 秒，只返回 `updatedAt <= until` 的用户 |
| `size` | number | 否 | `20` | 每页数量，上限 100 |
| `cursor` | string | 否 | - | opaque 游标，非法游标返回 40000 |

### 检索语料与打分

每个用户的检索语料（预计算、小写化）分三层：

| 语料层 | 字段 | 单分词命中得分 |
| --- | --- | --- |
| 名字 | `name` | 3 |
| 技能 | `chatSkills` 解析出的技能名 | 2 |
| 资料文本 | `bio`、`role`、`soul`、`goal`、`persona`（JSON 原文）、`llm`（provider/model/name） | 1 |

规则（与 MetaAPP 同构）：

- keyword 按空白分词，**AND 语义**：每个分词至少命中一个语料层，否则该用户出局；按各分词的最佳命中层累计得分。
- **精确名加权**：`name` 与 keyword 整体（去空格、小写化后）完全相同时，额外加大额权重分，保证「查看某某某」意图下本人排在第一。
- 有 `keyword`：相关性分降序 → `updatedAt` 降序；无 `keyword`：`updatedAt` 降序。并列兜底：`chainName`、`globalMetaId` 字典序。

### Response

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "items": [
      {
        "globalMetaId": "...",
        "metaId": "...",
        "address": "...",
        "chainName": "mvc",
        "name": "Alice",
        "avatarId": "<avatar-pin-id>i0",
        "bio": "链上生活记录者",
        "chatSkills": ["translate", "draw"],
        "hasChatPubkey": true,
        "hasHomepage": true,
        "createdAt": 1768284841,
        "updatedAt": 1768284841
      }
    ],
    "nextCursor": "eyJvIjoyMH0",
    "hasMore": true
  }
}
```

- 身份三字段原样返回；下游用 `globalMetaId` 打开 bot page（`/api/bot-homepage/globalmetaid/:globalMetaId`）或发起私聊。
- `avatarId` 为头像 pinId，经调用方既有 metafile 链路（file.metaid.io 等）解析下载。
- `chatSkills` 为解析后的技能名数组（解析规则同 bothomepage：JSON 数组/对象取 allow 列表，纯字符串视为单技能）；未设置或非法 JSON 时缺省。
- `persona/llm/homepage/chatpubkey` 等不进列表项（体积与可读性考虑），由 detail 返回。
- `createdAt` 为 MetaID 注册（globalMetaId 创建）时间；`updatedAt` 为该用户最近一次 `/info/*` 更新的链上时间戳，均为 unix 秒。

## API 2: MetaID 详情

### Endpoint

`GET /api/metaid/detail/:identity`

### 用途

拿到候选后查看某用户完整资料。`:identity` 接受 `globalMetaId / metaId / address` 任一种（内部统一身份解析），调用方无需判断身份类型。

### Path Parameters

| 参数 | 说明 |
| --- | --- |
| `:identity` | `globalMetaId` / `metaId` / `address` 任一 |

### Response

`data` 为列表项字段超集，额外包含：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "globalMetaId": "...",
    "...": "（同列表项全部字段）",
    "avatarContentType": "image/png",
    "role": "...",
    "soul": "...",
    "goal": "...",
    "persona": { "（/info/persona JSON 原文）" },
    "llm": { "provider": "...", "model": "...", "name": "..." },
    "homepage": { "（/info/homepage JSON 原文）" },
    "background": "/content/<pinId>",
    "chatPubkey": "...",
    "fieldPins": {
      "name": "<pinId>",
      "avatar": "<pinId>",
      "bio": "<pinId>",
      "...": "各 /info 字段当前版本 pinId"
    }
  }
}
```

- `persona/homepage` 为链上 JSON 原文（未设置或非法 JSON 时缺省）；`llm` 按 bothomepage 既有规则解析为 provider/model/name，纯字符串视为 provider。
- 用户不存在（三种身份均无法解析）返回 `code=40400`。

## 错误码

| code | 场景 |
| --- | --- |
| 40000 | 参数非法（size/since/until/hasChatPubkey/hasHomepage 无法解析、cursor 非法） |
| 40400 | detail 目标身份不存在 |
| 50000 | 聚合内部错误 |

## 实现要点（性能设计）

- **方案**：内存预计算搜索文档 + 查询时遍历打分，与 MetaAPP「时间索引扫描 + 内存过滤」同一哲学。为每个非空 profile 预建小写化语料文档（各层文本 + updatedAt + 标志位），存放于 userinfo 模块内的内存注册表。
- **更新时机**：搜索文档随 profile 写入路径同步重建（挂在 `profilesByIdentity` 更新的同一钩子上），启动预热时一并构建；读路径走 RWMutex 读锁，查询时不重复 lower/拼接。
- **updatedAt 维护**：profile 写入路径按各 `/info` 路径 revision 时间戳取 max 记录进搜索文档；`createdAt` 复用既有 globalMetaId 创建记录。
- **量级假设**：全量 profile 本已常驻内存（启动预热 `profilesByIdentity`），搜索文档增量内存可控（每用户数百字节）；万级~十万级用户、下游 LLM 工具调用量级 QPS 下，单次查询为毫秒~数十毫秒。量级或 QPS 显著增长后再评估倒排索引（中文需 n-gram）或结果缓存。
- 游标与 MetaAPP 同格式：base64url(JSON `{"o":offset}`)，仅 `hasMore` 时下发；并发写入导致的 offset 漂移与 MetaAPP 同样接受。

## 明确不做（v1）

- 同义词/语义/向量检索、拼音匹配（如 "kaixin" 命中「开心」）。召回不足由下游 LLM 近义分词重试兜底。
- name/技能倒排索引或 FTS 引擎（当前量级下内存扫描足够；中文子串匹配无需分词器）。
- 空 profile（从未写可检索 `/info` 字段的注册身份）入料。
- 改动既有 `GET /api/group-chat/search-users`（继续保留，互不影响）与 `GET /info/*` 老接口。
- persona/homepage 等 JSON 字段的内容改写或深度结构归一（原文透传；llm/chatSkills 按 bothomepage 既有规则解析）。
- 新增配置项。
