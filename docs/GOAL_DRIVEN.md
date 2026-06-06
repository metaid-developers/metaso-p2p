# metaso-p2p v1 — Goal-Driven 开发主文档

## 总目标（Goal）

构建 metaso-p2p v1：一个纯 PebbleDB、模块化聚合、多链索引的 MetaID 协议中间件。完成后 idchat 可直接对接使用（仅修改配置文件中的 URL，不改动任何业务逻辑源代码）。

## 总验收标准（Master Criteria）

master agent 在以下条件**全部满足**时判定开发完成：

1. **编译通过**：`go build ./...` 零错误零警告（Go 1.21+）。
2. **单元测试全绿**：`go test ./...` 全部 PASS，覆盖率 ≥ 60%。
3. **idchat 对接可用**：idchat 配置指向 metaso-p2p，完成以下 10 个场景无报错：
   a. 连接 Socket.IO → 收到 `heartbeat_ack`
   b. 群聊消息（链上确认后）→ 群成员收到 `WS_SERVER_NOTIFY_GROUP_CHAT`
   c. 私聊消息 → 对方收到 `WS_SERVER_NOTIFY_PRIVATE_CHAT`
   d. 查询群聊历史 → HTTP API 返回正确消息列表
   e. 查询私聊历史 → HTTP API 返回正确消息列表
   f. 查询用户信息 → `/api/info/*`（或 `/metafile-indexer/api/info/*`）返回 name/avatar/chatpubkey（字段名与 meta-file-system 对齐）
   g. 角色变更（管理员/拉黑/移出）→ 收到 `WS_SERVER_NOTIFY_GROUP_ROLE`
   h. 屏蔽聊天 → `/push-base/v1/push/add_blocked_chat`（POST，带签名）成功，`get_user_blocked_chats` 返回该记录
   i. 解除屏蔽 → `/push-base/v1/push/remove_blocked_chat` 成功，列表更新
   j. 搜索用户 → `/group-chat/search-users?query=X` 返回匹配列表
4. **HTTP 响应格式兼容**：所有端点返回 `{"code": 0, "data": ...}` 格式（成功），或 `{"code": <non-zero>, "message": "..."}`（错误）。404 场景返回 `{"code": 1, "message": "not found"}`。不允许返回裸 HTML、纯文本、或空 body。
5. **数据持久化**：重启 metaso-p2p 后，Pebble 中数据不丢失，索引高度从断点恢复继续扫描。
6. **无外部数据库**：仅需 PebbleDB 嵌入式引擎。不需要 MongoDB、MySQL、Redis 或任何外部数据库进程。
7. **多链就绪**：BTC 链完整可用（区块扫描 + ZMQ mempool）。MVC/DOGE/OPCAT 的 Chain+Indexer 接口已实现且编译通过。

## 架构概览

```
Chain RPC + ZMQ → Indexer Engine → Aggregator Registry
                                       ├── UserInfo     → Pebble + HTTP API
                                       ├── GroupChat    → Pebble + HTTP API + Socket Push
                                       ├── PrivateChat  → Pebble + HTTP API + Socket Push
                                       └── Notify       → Pebble + HTTP API
                                            │
                                       Socket.IO Server → idchat
```

详细模块规格：`docs/IMPLEMENTATION_PLAN.md`。idchat 接口契约：`docs/IDCHAT_API_CONTRACT.md`。

## 开发阶段（Phases）

每个 Phase 是一个子目标，master agent 创建独立 subagent 完成。Phase **严格按序执行**（Phase 2→3→4→5→6→7），不可并行。通过一个 Phase 的全部验收条款后才进入下一个 Phase。Phase 1 已完成，从 Phase 2 开始。

### Phase 2、3 的隐性依赖

Phase 4（GroupChat）的 Socket 推送验收需要 Phase 2（Socket）完成。Phase 4 的群聊数据入库验收需要 Phase 3（BTC Index）提供真实链上数据。因此即使 Socket 和索引理论上可并行，验收环境要求严格按序。

---

## Phase 1: 编译骨架 ✅ 已完成

**子目标**：项目骨架搭建完成，`go build ./...` 编译通过。

**当前状态**：已完成。14 个 Go 源文件就位。main.go 仅依赖 Phase 1 包，可独立编译。已确认无循环导入。

**包含的文件**：
- `cmd/metaso-p2p/main.go` — 入口（仅依赖 Phase 1 包）
- `internal/storage/pebble.go` — PebbleStore
- `internal/cache/cache.go` — 两级缓存
- `internal/chain/adapter.go` — Chain + Indexer 接口
- `internal/chain/bitcoin/bitcoin.go` — BTC RPC 客户端
- `internal/chain/bitcoin/indexer.go` — BTC 解析器骨架
- `internal/indexer/engine.go` — 索引引擎骨架
- `internal/aggregator/aggregator.go` — Aggregator 接口 + Registry
- `internal/aggregator/userinfo/module.go` — UserInfo 聚合器
- `internal/aggregator/groupchat/module.go` — GroupChat 占位
- `internal/aggregator/privatechat/module.go` — PrivateChat 占位
- `internal/aggregator/notify/module.go` — Notify 聚合器
- `internal/api/response.go` — 统一响应格式
- `internal/config/config.go` — 配置系统

---

## Phase 2: Socket.IO 服务器

**子目标**：实现 `internal/socket/`，idchat 可建立连接、维持心跳、接收推送。

**要创建/修改的文件**：
- `internal/socket/server.go` — Socket.IO server 创建与启动
- `internal/socket/manager.go` — 连接管理：多设备、心跳、超时清理
- `internal/socket/push.go` — `{M, C, D}` 格式消息发送
- `internal/socket/presence.go` — 在线状态查询
- `internal/socket/server_test.go` — Socket 测试
- `internal/api/router.go` — 中央 Gin 路由
- `cmd/metaso-p2p/main.go` — 集成 socket server

**验收条款**：

- [ ] **编译**：`go build ./...` 通过。
- [ ] **连接**：用 socket.io-client v4 连接 `ws://localhost:<port>/socket/socket.io?metaid=test123&type=pc`，收到 `connect` 事件。
- [ ] **type=app 连接**：同样参数但 `type=app`，收到 `connect` 事件。
- [ ] **心跳**：客户端发送 `socket.emit('ping')`，10 秒内收到服务端 `heartbeat_ack` 事件（请求-响应模式，非服务端主动推送）。
- [ ] **PC 设备限制**：同一 metaid，type=pc 连接第 4 次时，最旧的连接被断开（或第 4 次被拒绝）。
- [ ] **App 设备限制**：同一 metaid，type=app 连接第 4 次时，同样触发限制。
- [ ] **推送格式**：`SendMessageToUser(metaid, {M: "TEST", C: 0, D: "hello"})`。客户端 `message` 事件收到 `{"M":"TEST","C":0,"D":"hello"}`（注意 `C` 是数字不是字符串）。
- [ ] **房间广播**：3 个客户端 JoinRoom("group:X")，服务端对房间发消息，3 个客户端全部收到。
- [ ] **优雅关闭**：SIGTERM 后，所有连接正常断开，Pebble 正确关闭（日志中出现 "stopped cleanly"）。
- [ ] **超时清理**：客户端 35 秒不发 ping，服务端自动断开该连接。
- [ ] **统计查询**：`GET /socket/online/stats` 返回 `{"code":0,"data":{"totalConnections":N}}`。
- [ ] **在线列表**：`GET /socket/online/list?page=1&size=20` 返回 `{"code":0,"data":{"items":[...]}}`。

**参考代码**：
- `show-now-tmp/common/socket_util/socket_manager.go`（连接管理逻辑，文件位于 show-now-tmp 仓库）
- 旧 metaso-p2p 的 socket 实现已随 cleanup 删除，参考 show-now-tmp 即可

---

## Phase 3: BTC 链索引 + UserInfo 端到端

**子目标**：BTC 链适配器完整实现 + 索引引擎运行 + UserInfo 聚合器入库。

**要创建/修改的文件**：
- `internal/chain/bitcoin/indexer.go` — 完善 `CatchPins`/`CatchPinsByTx`，集成 metaid-script-decoder
- `internal/indexer/engine.go` — 完善 ZMQ 监听、transfer 处理、错误重试
- `internal/aggregator/userinfo/module.go` — 根据实际 Pin 格式微调
- `pkg/idaddress/` — 从 show-now-tmp 移植 GlobalMetaId 编解码（`idaddress.go`, `bech32.go`, `converter.go`）
- `internal/api/router.go` — 注册 UserInfo 路由
- `cmd/metaso-p2p/main.go` — 集成索引引擎启动
- `internal/chain/bitcoin/indexer_test.go` — BTC CatchPins 单元测试
- `internal/indexer/engine_test.go` — 引擎集成测试

**验收条款**：

- [ ] **编译**：`go build ./...` 通过。
- [ ] **BTC 区块解析**：用已知 BTC 测试网区块高度（如高度 100），`CatchPins(100)` 返回非空 Pin 列表。Pin 的 `Path`、`Operation`、`ContentBody`、`ChainName` 字段不为空。`ChainName == "btc"`。
- [ ] **BTC mempool**：ZMQ 连接 BTC 节点。向测试网广播一笔含 MetaID 数据的交易，30 秒内索引引擎通过 `HandleMempoolPin` 处理该 Pin（超时可适当放宽，但 master agent 应确保不是永久阻塞）。
- [ ] **UserInfo 入库**：索引一个包含 `/` (init) 和 `/info/name` 的区块后，通过 PebbleStore 验证：`Get("userinfo", []byte("profile:<metaid>"))` 返回包含 `"name"` 字段的非空 JSON。
- [ ] **UserInfo HTTP**：`GET /api/info/metaid/<metaid>` 返回 `{"code":1,"data":{"name":"...","avatar":"...","chatpubkey":"..."}}`，字段名与 meta-file-system 对齐（小写 `chatpubkey` / `chatpubkeyId`）。同样路由也挂在 `/metafile-indexer/api/info/metaid/...` 下作为 idchat `metafileIndexerApi` 客户端的 drop-in 替换，`code: 1` 成功 / `40400` not_found / `40000` invalid_param 三种返回与 meta-file-system 一致。
- [ ] **高度恢复**：重启进程后，`Get("indexer_meta", []byte("btc_lastheight"))` 返回之前的高度。引擎从该高度+1 开始扫描，不重复索引已有区块。
- [ ] **GlobalMetaId**：`/api/info/address/<addr>` 返回的 `globalMetaId` 字段以 `id-` 前缀开头。
- [ ] **缓存命中**：同一 metaid 调两次 `/api/info/metaid/<id>`，第二次耗时 < 第一次的 1/10（Pebble 磁盘 IO vs 内存 LRU 的差距足够显著）。
- [ ] **缓存失效**：处理新 `/info/name` Pin 更新用户名后，立即查询该用户信息，返回更新后的 name（非旧缓存值）。

**参考代码**：
- `show-now-tmp/adapter/bitcoin/indexer.go` — CatchPins 完整解析逻辑
- `man-p2p/adapter/bitcoin/` — 更现代的 BTC 索引器结构
- `show-now-tmp/idaddress/` — GlobalMetaId 编解码（直接移植）

---

## Phase 4: GroupChat 聚合器

**子目标**：群聊完整生命周期 — Pin → Pebble → HTTP 查询 → Socket 推送。

**要创建/修改的文件**（9 个）：
- `internal/aggregator/groupchat/module.go` — 重写为完整 Aggregator 实现
- `internal/aggregator/groupchat/process.go` — Pin 处理：community/group/chat 三类协议分发
- `internal/aggregator/groupchat/db_community.go` — 社区 Pebble CRUD
- `internal/aggregator/groupchat/db_group.go` — 群组 Pebble CRUD
- `internal/aggregator/groupchat/db_chat.go` — 群聊消息 Pebble CRUD（含游标分页）
- `internal/aggregator/groupchat/api.go` — HTTP 路由注册（约 25 个端点）
- `internal/aggregator/groupchat/notify.go` — NotifyEvent 生成
- `internal/aggregator/groupchat/process_test.go` — 群聊 Pin 处理单元测试
- `internal/socket/push.go` — 消费 NotifyEvent channel 并实际推送

**验收条款**：

- [ ] **编译**：`go build ./...` 通过。
- [ ] **群组创建**：处理 `simplegroupcreate` Pin 后，`GET /group-chat/group-info?groupId=<id>` 返回群组名称、创建者 metaid。
- [ ] **群聊消息入库**：处理 `simplegroupchat` Pin 后，`GET /group-chat/group-chat-list-v2?groupId=<id>&cursor=&size=20` 返回含该消息的列表。消息字段 content、protocol、timestamp、metaId 与 IDCHAT_API_CONTRACT.md 一致。
- [ ] **分页查询**：入库 100 条消息，size=20 逐页查询。每页 `nextCursor` 非空（末页为空或 `""`）。5 页消息无重复无遗漏。注意：游标分页不要求 `total` 字段，仅要求可遍历完整数据集。
- [ ] **索引查询**：`GET /group-chat/group-chat-list-by-index?groupId=<id>&startIndex=0&size=20` 返回最新 20 条（按 timestamp 降序）。
- [ ] **Socket 推送**：处理新消息 Pin 后，已加入该群房间的客户端 5 秒内收到 `message` 事件，`M` 为 `WS_SERVER_NOTIFY_GROUP_CHAT`，`D` 含 groupId、content、userInfo。
- [ ] **角色变更推送**：处理 `simplegroupadmin` Pin 后，被设置的用户收到 `WS_SERVER_NOTIFY_GROUP_ROLE`，`isAdmin=true`。处理 `simplegroupblock` Pin 后 `isBlocked=true`。
- [ ] **用户搜索**：`GET /group-chat/search-users?query=Alice` 返回匹配列表，每条含 metaId、userName、avatar。
- [ ] **群成员查询**：`GET /group-chat/group-member-list?groupId=<id>` 返回成员列表，creator 和 isAdmin 正确标注。
- [ ] **社区查询**：`GET /group-chat/community/list?page=1&pageSize=20` 返回分页社区列表。社区创建（`simplecommunity` Pin）后能查到。
- [ ] **HTTP 响应格式**：所有 GroupChat 端点返回 `{"code":0,"data":...}`。不存在的群组返回 `{"code":1,"message":"not found"}`。

**参考代码**：
- `show-now-tmp/basicprotocols/group_chat/db/group_and_channel_db.go` — GroupDB 方法（群组 CRUD、成员管理）
- `show-now-tmp/basicprotocols/group_chat/db/chat_db.go` — ChatDB 方法（消息 CRUD）
- `show-now-tmp/basicprotocols/group_chat/db/community_db.go` — CommunityDB 方法
- `show-now-tmp/basicprotocols/group_chat/service/chat_ws.go` — Socket 推送集成

---

## Phase 5: PrivateChat 聚合器

**子目标**：私聊完整生命周期 — Pin → Pebble → HTTP 查询 → Socket 推送。

**要创建/修改的文件**（5 个）：
- `internal/aggregator/privatechat/module.go` — 重写为完整 Aggregator
- `internal/aggregator/privatechat/process.go` — 私聊 Pin 处理
- `internal/aggregator/privatechat/db.go` — 私聊 Pebble CRUD（含游标分页）
- `internal/aggregator/privatechat/api.go` — HTTP 端点（4 个）
- `internal/aggregator/privatechat/process_test.go` — 测试

**验收条款**：

- [ ] **编译**：`go build ./...` 通过。
- [ ] **消息入库**：处理 `simplemsg` Pin 后，`GET /group-chat/private-chat-list?otherMetaId=<对方>&metaId=<我>&cursor=&size=20` 返回含该消息的列表。
- [ ] **分页查询**：入库 50 条双向私聊。A 查 B 的列表只含 A-B 间的消息，B 查 A 的列表只含 B-A 间的消息，无交叉泄漏。
- [ ] **索引查询**：`GET /group-chat/private-chat-list-by-index?otherMetaId=<对方>&metaId=<我>&startIndex=0&size=20` 返回最新消息。
- [ ] **私聊首页**：`GET /group-chat/chat/homes/<metaId>` 返回该用户的私聊会话列表，每个会话含最后一条消息预览。
- [ ] **路径查询**：`GET /group-chat/private-group-paths?metaId=<id>` 返回路径列表。
- [ ] **Socket 推送**：处理私聊 Pin 后，收件人客户端 5 秒内收到 `WS_SERVER_NOTIFY_PRIVATE_CHAT`，含 fromUserInfo、content。
- [ ] **拉黑处理**：处理 `simpleprivateblock` Pin 后，被拉黑用户通过 `GET /push-base/v1/push/get_user_blocked_chats?metaId=<id>` 能看到该记录。

---

## Phase 6: 全链适配 — MVC/DOGE/OPCAT

**子目标**：MVC、DOGE、OPCAT 的 Chain+Indexer 完整可用。

**要创建/修改的文件**（每个链 2 个源文件 + 1 测试）：
- `internal/chain/mvc/mvc.go` + `internal/chain/mvc/indexer.go` + `internal/chain/mvc/indexer_test.go`
- `internal/chain/dogecoin/dogecoin.go` + `internal/chain/dogecoin/indexer.go` + `internal/chain/dogecoin/indexer_test.go`
- `internal/chain/opcat/opcat.go` + `internal/chain/opcat/indexer.go` + `internal/chain/opcat/indexer_test.go`

**验收条款**：

- [ ] **编译**：`go build ./...` 通过。所有链的 Chain 和 Indexer 接口完整实现（无 stub，无 no-op 空方法体）。
- [ ] **MVC CatchPins**：连接 MVC 测试网节点，`CatchPins(knownHeight)` 返回非空 Pin 列表。Pin 的 `ChainName == "mvc"`。
- [ ] **DOGE CatchPins**：连接 DOGE 测试网节点。**必须使用包含 AuxPoW 的已知高度**（DOGE 测试网上 AuxPoW 区块是常态）。`GetBlock(auxPoWHeight)` 不报错，`CatchPins` 返回非空列表。Pin 的 `ChainName == "doge"`。
- [ ] **OPCAT CatchPins**：连接 OPCAT 节点，`CatchPins(knownHeight)` 返回非空 Pin 列表。Pin 的 `ChainName == "opcat"`。使用 blob-based 解析（OP_RETURN 内的 header+body 格式），不是标准 SegWit。
- [ ] **Mempool 全链**：每条链启动 ZMQ 后，`GetMempoolTransactionList()` 能成功调用 RPC（返回空列表也算成功，只要不报错）。
- [ ] **地址提取**：每条链的 `GetAddress(pkScript)` 返回对应网络的有效地址字符串。

**参考代码**（锁定到当前仓库 HEAD）：
- MVC：`show-now-tmp/adapter/microvisionchain/`
- DOGE：`man-p2p/adapter/dogecoin/`（AuxPoW 逐字节解析）和 `show-now-tmp/adapter/dogecoin/`（三种铭文格式解析）
- OPCAT：`show-now-tmp/adapter/opcat/`（blob parser + `ParseOpcatPin` + GetBlock 回退机制）和 `man-p2p/adapter/opcat/`（`GetMempoolTransactionList` 补全）

---

## Phase 7: idchat 对接验证与部署

**子目标**：端到端验证。部署文档。Docker 镜像。

**要创建/修改的文件**：
- `Dockerfile` — 多阶段构建
- `config.example.toml` — 完整配置示例
- `DEPLOY.md` — 部署文档
- `docs/IDCHAT_CONFIG_CHANGE.md` — idchat 对接时需修改的配置项清单

**验收条款**：

- [ ] **Docker 构建**：`docker build -t metaso-p2p .` 成功。最终镜像基于 `scratch` 或 `alpine`，大小 < 50MB（实测 arm64 镜像约 40MB，主要来自 Go 静态二进制 ~30MB；进一步压缩需 UPX 或 scratch base，留待后续优化）。
- [ ] **配置文档**：按 `DEPLOY.md` 操作，5 分钟内能在新机器上启动 metaso-p2p。
- [ ] **idchat 对接**：修改 idchat 配置指向 metaso-p2p。完成 Master Criteria 中全部 10 个场景无报错。
- [ ] **idchat 改动范围**：允许修改 idchat 的 `config.json`（URL 字段）和环境变量。**禁止**修改 idchat 的任何 `.ts` / `.tsx` / `.vue` / `.js` 源文件。
- [ ] **idchat 配置清单**：`docs/IDCHAT_CONFIG_CHANGE.md` 逐一列出需要修改的配置项及其新值。
- [ ] **多链独立启停**：修改配置可分别启用/禁用每条链的索引和 ZMQ，不互相影响。

---

## Subagent 工作规范

1. **独立完成 Phase**：收到一个 Phase 的子目标后，独立创建/修改所有相关文件。
2. **TDD**：先写测试，后写实现。测试必须在 `go test` 中 PASS。
3. **提交附带验收报告**：在 commit message 中列出本 Phase 所有验收条款及其完成状态（勾选或注明 `SKIP` 及原因）。
4. **不破坏已有接口**：不修改已通过验收的模块的公开接口。如需修改，先在 commit message 中说明原因，供 master agent 评估。
5. **适配 metaso-p2p 架构**：参考代码标注在验收条款中。subagent 必须适配到 PebbleDB + 事件总线 + Aggregator 接口，不可直接复制 show-now-tmp 的 MongoDB 调用或回调模式。
6. **固定参考 commit**：show-now-tmp 参考 commit `1643a1a`，man-p2p 参考 commit `HEAD`（截至 2026-05）。meta-file-system 参考 commit `HEAD`。

## Master Agent 工作规范

1. **逐条验收，不可跳过**：每个 Phase 的验收条款逐条执行。一条不通过 → subagent 继续修改。
2. **5 分钟活跃度检查**：subagent 超 5 分钟无响应或无代码提交 → 主动检查状态。如卡住，用相同 Phase 子目标重建 subagent。
3. **master 不写代码**：只做验收和调度。发现问题 → 让 subagent 修复，不直接改代码。
4. **Phase 严格顺序**：Phase 2→7 不可并行。每个 Phase 必须在上一 Phase 全部验收通过后才能开始。
5. **最终验收**：Phase 7 完成后，对照 Master Criteria 逐一验收。全部通过才宣布 v1 完成。

---

## Phase 8 (post-v1): Bot Hub Skill-Service 聚合 API

**子目标**：为 IDBots Bot Hub 前端提供 `/api/bot-hub/skill-service/*` 聚合接口。完整 wire contract 见 `docs/specs/2026-05-28-bot-hub-skill-service-aggregation-api.md`。

**关键设计原则**：
- 聚合端只输出声明事实和可验证统计，不输出 `canOrder` / `available` / `disabledReason` 等动作许可裁决字段。
- v1 强制同链版本链：create/modify/revoke 必须在同一 `chainName` 下；跨链 modify/revoke 等协议层补 `originalChainName` 后再支持。
- 走 metaso-p2p 主体响应约定（`code=0` success，错误码 `40000/40400/50000`），不复用 `/api/info/*` 的 `code=1` 兼容模式。

**Milestone（每个独立 commit + buzz）**：

| Milestone | 内容 | 验收 |
| --- | --- | --- |
| M0 | spec 落地 + README / CLAUDE / GOAL_DRIVEN 更新 | spec 在 `docs/specs/`；项目说明提及 Bot Hub |
| M1 | `internal/aggregator/skillservice/` 模块骨架；索引 `/protocols/skill-service`；`originalId` 同链折叠；PebbleDB 持久化当前服务视图 | 构造同链 create+modify+revoke pin → Pebble 中只剩 latest；revoke 默认不可见；缺失 `originalId` 走 fallback；跨链 originalId 不折叠并记 fallback 日志 |
| M2 | provider profile 接入；in-process 读 `userinfo` 聚合；补 `providerName/providerAvatar/providerChatPubkey` | request path 不调外部 manapi；profile 缺失透传 null 不转裁决码 |
| M3 | `/protocols/skill-service-rate` 索引与评分聚合；avg/count；`serviceID` 反查归一到 `sourceServicePinId` | 单测覆盖 source id / current id / 旧版本 id 三种 rating 输入 |
| M4 | asset URL 解析；`METASO_P2P_ASSET_BASE_URL` 配置 | serviceIcon/providerAvatar 都返回可加载 URL；已是 http(s) URL 时原样透传 |
| M5 | `GET /api/bot-hub/skill-service/list`；filter/sort/cursor paginate；错误码 envelope；`code=0 success` | 集成测试覆盖筛选、排序、分页、错误 code、`includeInactive` 边界 |
| M6 | `GET /api/bot-hub/skill-service/detail/:serviceId`；`service` + `provider` 字段；`mrc20Ticker/Id` MRC20 路径 | 集成测试覆盖 currentPinId / sourceServicePinId 查询、缺失声明字段透传、MRC20 详情；不返回动作许可判断 |

**第一阶段交付到 M6**。ratings 分页 / revisions 分页 / 订单统计 / 退款风险 / relatedServices / request schema 留到后续，明确产品需要再做。

**前置依赖**：
- `cmd/metaso-p2p/main.go` 必须 wire MVC indexer 才能真链验证（M5/M6 集成测试前补即可，M1-M4 用构造的 PIN 数据走单测）。
- skill-service 协议在 `internal/aggregator/` 注册顺序：现有 4 个 aggregator 之后追加 `skillservice.Aggregator`。
