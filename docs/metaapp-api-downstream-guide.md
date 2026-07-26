# MetaApp 聚合 API 下游对接指南

面向 IDBots 等 AI 宿主的接入说明。完整契约以 [`docs/specs/2026-07-26-metaapp-query-api.md`](specs/2026-07-26-metaapp-query-api.md) 为准，本文只讲怎么用。

## 基本信息

- 生产 Base URL：`https://so.metaid.io`（`https://socket.metaid.io` 为兼容入口，等价）
- 响应封装：`{code, data, message, processingTime}`，成功 `code=0`；业务错误 `40000`（参数/游标非法）、`40400`（不存在）、`50000`（内部错误）；HTTP 恒 200
- 三个接口：
  - `GET /api/metaapp/list` — 列表 / 检索（核心）
  - `GET /api/metaapp/detail/:pinId` — 完整 manifest（含 `prompt` 与原始 `payload`）
  - `GET /api/metaapp/forks/:pinId` — 直接子代派生列表
- 打开应用：用返回的 `pinId` 构造 `metaapp://<pinId>`，走宿主现有 MetaApp 打开链路（IDBots 内即 `bot_browser_open_uri`）；`content`（`metafile://....zip`）+ `indexFile` 也可自行下载渲染
- 时间字段为 unix 秒；游标分页用返回的 `nextCursor` 原样回传

## 典型意图 → 参数速查

| 用户说法 | 调用 |
| --- | --- |
| 「最近七天的小游戏」 | `list?keyword=小游戏&since=<now-7d>&size=10` |
| 「找做笔记的应用」 | `list?keyword=笔记&size=10` |
| 「能显示 simplebuzz 的应用」 | `list?tag=simplebuzz`（兜底 `keyword=simplebuzz`） |
| 「打开某 MetaID 最新的应用」 | `list?publisher=<metaId|globalMetaId|address>&size=1` |
| 「某人发布过哪些应用」 | `list?publisher=<...>&size=20` |
| 「基于这个应用派生出哪些」 | `forks/<pinId>`（子代的子代：对子代再调一次） |
| 「最新发布的应用」 | `list?size=10`（无条件即按更新时间倒序） |

要点：

- **自然语言时间由宿主换算**：LLM 把「最近七天」折算成 `since`（unix 秒）再传，接口不理解自然语言。
- `keyword` 是空格分词 AND 语义，命中 title/appName/intro/tags；排序是相关性分（tag×3 > title×2 > intro×1）+ 更新时间倒序。
- `publisher` 同时匹配发布者的 globalMetaId / metaId / address 三个字段，传哪个都行。
- detail/forks 的 `:pinId` 接受版本链上任意版本 pinId，自动解析到最新记录。
- 结果里 `disabled=true` 的默认不出现；`revoke` 的恒不出现。

## search_metaapps 工具建议（IDBots 侧）

参照现有 `botBrowserAgentTools.ts` 的 inline MCP 工具模式新增一个工具：

```ts
tool(
  "search_metaapps",
  "Search on-chain MetaApps (HTML mini-apps published via /protocols/metaapp). " +
  "Use when the user wants to FIND/DISCOVER an app by intent, topic, time range, or publisher — " +
  "rather than open a known app. Returns up to `limit` candidates; pick the best match and open it " +
  "with bot_browser_open_uri using metaapp://<pinId>. For remix lineage of a known app, " +
  "call with mode='forks' and its pinId.",
  {
    query: z.string().optional(),     // 自然语言检索词，会被透传为 keyword
    tag: z.string().optional(),       // 协议/能力标签，如 simplebuzz
    publisher: z.string().optional(), // metaId / globalMetaId / address
    sinceDays: z.number().optional(), // 宿主换算成 since
    mode: z.enum(["search", "forks"]).optional(),
    pinId: z.string().optional(),     // mode=forks 时必填
    limit: z.number().optional(),     // 默认 8，最大 20
  },
  handler,
)
```

handler 侧建议：

- **裁剪后再喂回 LLM**。完整 item 字段较多，工具返回时建议每条只保留 `pinId / title / appName / intro / tags / runtime / version / updatedAt / publisherGlobalMetaId / forkedFrom`，纯文本或紧凑 JSON 列表（仿 `formatBotBrowserTabs` 风格），控制上下文体积。
- **空结果降级**：`keyword` 无结果时，去掉分词中较弱的一个重试一次；仍无则回报「链上暂无匹配应用」，不要编造。
- **路由提示词**：在 system prompt 加一条——「用户想找/发现某类应用（而非打开已知应用）时，先调 `search_metaapps`；从候选中选一个后用 `bot_browser_open_uri` 以 `metaapp://<pinId>` 打开；用户问某应用的派生/二创时用 `search_metaapps` 的 forks 模式」。
- 「最近 N 天」类意图由工具实现把 `sinceDays` 换算成 `since = now - N*86400`。
- 「打开 TA 最新的应用」：`publisher + limit=1`，取第一条直接打开。

## 错误与边界

- `40000`：检查 size/cursor/since 参数；游标必须原样回传，不要自行构造。
- `40400`：detail/forks 目标不存在（或已被 revoke），按「应用不存在或已删除」处理。
- forks 只返直接子代，不递归整棵树。
- `icon/coverImg/content` 返回 `metafile://` URI，经宿主既有 metafile 链路（file.metaid.io 等）解析下载。
- 列表最多返回 100 条/页；意图检索建议 `size=5~10`。

## 能力标签约定（发布侧配合）

「能显示 simplebuzz」「能发布链上笔记」这类能力检索依赖发布时在 payload `tags` 里声明能力标签（建议直接用协议名，如 `simplebuzz`、`simplenote`）。未声明的应用只能靠 intro 文本兜底命中。IDBots 的发布工具（`bot_browser_publish_app` 链路）生成 metaapp payload 时应把应用支持的协议写进 `tags`。
