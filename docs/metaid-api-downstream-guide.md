# MetaID 聚合搜索 API 下游对接指南

面向 IDBots 等 AI 宿主的接入说明。完整契约以 [`docs/specs/2026-07-28-metaid-search-api.md`](specs/2026-07-28-metaid-search-api.md) 为准，本文只讲怎么用。与 MetaAPP 聚合 API（[`metaapp-api-downstream-guide.md`](metaapp-api-downstream-guide.md)）是同一套约定，学会一套即可。

## 基本信息

- 生产 Base URL：`https://so.metaid.io`（`https://socket.metaid.io` 为兼容入口，等价）
- 响应封装：`{code, data, message, processingTime}`，成功 `code=0`；业务错误 `40000`（参数/游标非法）、`40400`（不存在）、`50000`（内部错误）；HTTP 恒 200
- 两个接口：
  - `GET /api/metaid/list` — 用户列表 / 检索（核心）
  - `GET /api/metaid/detail/:identity` — 完整资料（`:identity` 接受 globalMetaId / metaId / address 任一种，自动解析）
- 拿到候选后的动作：
  - **打开 bot page**：用返回的 `globalMetaId` 走宿主现有 bot page 链路（IDBots 内即 bot browser 的 bot page 打开方式；数据侧对应 `GET /api/bot-homepage/globalmetaid/:globalMetaId`）
  - **发私信**：筛 `hasChatPubkey=true` 的候选（detail 里的 `chatPubkey` 可取原始公钥），走宿主既有私聊链路
- 时间字段（`createdAt/updatedAt/since/until`）为 unix 秒；游标分页用返回的 `nextCursor` 原样回传
- 检索语料为 `name / bio / role / soul / goal / persona / chatSkills / llm` 的文本；注册后从未写过这些字段的「空用户」不会出现

## 典型意图 → 参数速查

| 用户说法 | 调用 |
| --- | --- |
| 「查看某某某的 bot page」 | `list?keyword=某某某&size=5` → 取第一条（精确名加权保证本人最前），用 `globalMetaId` 打开 bot page |
| 「某某某的具体信息」 | 先 `list?keyword=某某某&size=1` 拿身份，再 `detail/<globalMetaId>`；已知身份直接 `detail/<metaId|globalMetaId|address>` |
| 「找性格活泼开朗的用户聊天」 | `list?keyword=开朗&hasChatPubkey=1&size=10` |
| 「找会做翻译的 bot」 | `list?skill=translate&size=10`（兜底 `keyword=translate`） |
| 「最近一周新活跃的用户」 | `list?since=<now-7d>&size=10` |
| 「某条链上的用户」 | `list?chainName=mvc&size=10` |
| 「最新更新的用户」 | `list?size=10`（无条件即按 `updatedAt` 倒序） |

要点：

- **自然语言时间由宿主换算**：LLM 把「最近一周」折算成 `since`（unix 秒）再传，接口不理解自然语言。
- `keyword` 是空格分词 AND 语义，大小写不敏感子串匹配，中文无需分词；排序是相关性分（name×3 > chatSkills×2 > 资料文本×1）+ 精确名大额加权 + `updatedAt` 倒序。
- **精确名加权**：`keyword` 整体与某个用户的 `name` 完全相同（忽略大小写和空格差异）时该用户必排第一，「查看某某某」场景直接取第一条即可。
- `detail/:identity` 三种身份混用：LLM 拿到哪种就传哪种，无需判断类型。
- `hasChatPubkey=1` 是「能私聊」的前置条件，聊天类意图建议默认带上。

## search_metaid_users 工具建议（IDBots 侧）

参照 metaapp 的 `search_metaapps` 模式新增一个 inline MCP 工具：

```ts
tool(
  "search_metaid_users",
  "Search on-chain MetaID users/bots by name, persona, bio, or chat skills. " +
  "Use when the user wants to FIND/VIEW a person's bot page or profile, or discover users " +
  "to chat with — e.g. 'view <name>'s bot page', 'find cheerful users to chat with'. " +
  "Returns up to `limit` candidates with identity fields; open the best match's bot page " +
  "with its globalMetaId, or start a private chat when hasChatPubkey is true.",
  {
    query: z.string().optional(),        // 自然语言检索词，透传为 keyword（名字/性格/简介）
    skill: z.string().optional(),        // 技能名，如 translate / draw
    chatOnly: z.boolean().optional(),    // true 时透传 hasChatPubkey=1
    chainName: z.string().optional(),    // mvc / btc / doge / opcat
    sinceDays: z.number().optional(),    // 宿主换算成 since
    limit: z.number().optional(),        // 默认 8，最大 20
  },
  handler,
)
```

handler 侧建议：

- **裁剪后再喂回 LLM**。每条只保留 `globalMetaId / name / bio / chatSkills / hasChatPubkey / hasHomepage / updatedAt`（展示用名字而非 globalMetaId，人类可读性更好），紧凑 JSON 列表，控制上下文体积。
- **空结果降级**：`keyword` 无结果时，让 LLM 换近义词/去掉较弱分词重试一次（如「开朗」换「活泼」「外向」）；仍无则回报「链上暂无匹配用户」，不要编造。接口不做同义词扩展，召回靠宿主语义层兜底。
- **路由提示词**：在 system prompt 加一条——「用户想查看某人的 bot page 或资料、或想找某类人/会某技能的 bot 时，先调 `search_metaid_users`；从候选中选一个后用其 `globalMetaId` 打开 bot page；涉及私聊的意图优先 `chatOnly=true`」。
- 「最近 N 天」类意图由工具实现把 `sinceDays` 换算成 `since = now - N*86400`。
- 已知明确身份（对话上下文里已有 metaId/address/globalMetaId）时跳过 list，直接调 `detail/<identity>`。

## 错误与边界

- `40000`：检查 size/since/until/hasChatPubkey/hasHomepage/cursor 参数；游标必须原样回传，不要自行构造。
- `40400`：detail 目标身份不存在（三种身份形式都无法解析），按「用户不存在」处理。
- `avatarId` 为头像 pinId，经宿主既有 metafile 链路（file.metaid.io 等）解析下载；detail 的 `avatarContentType` 可辅助判断格式。
- `persona/homepage` 在 detail 里是链上 JSON 原文（未设置或非法 JSON 时字段缺省），聚合端不做内容改写。
- 空用户（注册后从未写 name/bio/persona 等可检索字段）不在语料内，list 任何条件下都不返回；但已知身份时 detail 仍可查到。
- 列表最多返回 100 条/页；意图检索建议 `size=5~10`。
- 检索为子串匹配，不做同义词/拼音/语义扩展（「kaixin」不会命中「开心」）。

## 联调自检（部署包含本功能的版本后可直接验证）

```bash
# 最新活跃用户（看真实返回结构）
curl -sS 'https://so.metaid.io/api/metaid/list?size=5'

# 关键词检索（名字/persona/bio 语料）
curl -sS 'https://so.metaid.io/api/metaid/list?keyword=开朗&size=5'

# 可私聊 + 技能过滤
curl -sS 'https://so.metaid.io/api/metaid/list?skill=translate&hasChatPubkey=1&size=5'

# detail（三种身份形式等价）
curl -sS 'https://so.metaid.io/api/metaid/detail/<globalMetaId>'

# 404 形态
curl -sS 'https://so.metaid.io/api/metaid/detail/no-such-user'
```

## 发布侧配合（资料完整度）

检索质量取决于用户在链上写的 `/info/*` 资料：名字检索靠 `/info/name`，性格/人设检索靠 `/info/persona`、`/info/bio`、`/info/role` 等，技能检索靠 `/info/chatskills`（建议写 JSON 数组或 `{"allow": [...]}` 结构，纯字符串按单技能处理）。「找人聊天」类场景还要求对方设置了 `/info/chatpubkey`。宿主在为 Bot 生成链上资料时应尽量把 persona、bio、chatskills 写完整。
