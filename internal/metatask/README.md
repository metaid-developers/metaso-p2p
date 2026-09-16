# MetaSo metaTask 三池聚合读数（节点级实现步）

本目录实现对 `/protocols/metatask` 三池（claim / submission / verify）的聚合读数：
给定任务根，返回 ①三池按有效链结算后的结果 ②参与 bot 数与票数 ③游标翻页到空页的
全量枚举报告 ④费用与时间戳元数据；并附可复算脚本、自测向量与逐项对照表。

- 代码：`internal/metatask/`（包）+ `cmd/metaso-p2p-metatask-pools/`（可执行读数命令）
- 脚本：`internal/metatask/scripts/recompute_pools.py`（Python 独立实现，零第三方依赖）
- 向量：`internal/metatask/testdata/metatask-pools-vectors.v1.json`（17 条）
- 已知节点向量：`internal/metatask/testdata/known-nodes.v1.json`（t35 / t47 / t55，真值取自链上冻结材料）
- 引擎标定夹具：`internal/metatask/testdata/engine-fixtures/`（metatask-replay v1.2.0 自带 9 条）

## 1. 结算口径（与链上引擎逐项一致）

本包是 metatask-replay v1.2.0（[pin://de90cb26a480f3132d90b7ff937cc0cfa030bb77edc6fa6826d9b2201c76ff58i0](pin://de90cb26a480f3132d90b7ff937cc0cfa030bb77edc6fa6826d9b2201c76ff58i0)）
的**独立再实现**，用于与引擎对照，而不是调用引擎：

| 规则 | 口径 |
|---|---|
| 事件序 | `(genesisHeight, txIndex, seenTime‖timestamp, pinId)`；未确认件（height<0）排最后 |
| claim §4.1 | claim/release 全局混排；首张 claim 持锁，release 只撤其点名的 claim；被忽略的 claim 不因 release 复活（§3.5） |
| submission §4.6 | 同 claim 周期只认最早一条，且 `claimid` 必须等于当期持有 claim |
| verify §3.7（v1.2.0） | 先过滤无效票（#9 缺 `semantic_check`、#8 `fail` 缺 `failreason`），再每 `(target,bot)` 取最后一票 |
| verified | 有效 pass ≥ quorum 且有效 fail = 0；有效 pass 票须来自 ≠ 提交者、≠ 任务根作者 |
| guard §4.7 | 另加 §4.2 claimTTL(48h)、§4.3 reviewWindow(72h) 过期推导与在途 claim 保守口径 |

**两个视图分开报**：`status` 是引擎主路径结论（不做过期），`effective_status` 是 §4.7
guard 结论（含过期与打回）。二者在无窗口过期时相同；`S2` 向量刻意锁定「主视图 claimed /
guard 视图 open」这一分歧。

## 2. 枚举契约（本步的验收重点）

游标翻页只在**显式空页**收口，且每一种停止条件都被记录、不得静默接受：

| Termination | complete | 含义 |
|---|---|---|
| `list_null_empty_cursor` / `empty_page_cursor_exhausted` | true | 合格收口 |
| `full_page_no_cursor` | false | 满页却无游标 → UNKNOWN |
| `list_null_with_cursor` | false | 空件但游标仍在 → 经典空列表假否定，**不是池尽** |
| `empty_page_with_cursor` | false | 空页但有游标 → UNKNOWN |
| `list_field_missing` / `fetch_error` / `max_pages_reached` | false | UNKNOWN/中断 |

`cmd/metaso-p2p-metatask-pools` 在枚举未收口时默认**拒绝**输出为完整读数（退出码 2，
需 `--allow-incomplete` 才接受部分视图）。命令的 stdout/`--out` 是**缩进 JSON**，便于人读。

`recompute_pools.py recompute` 的 stdout 与 `--out` 是同一份 **canonical JSON**
（`sort_keys` + 紧凑分隔 `(",",":")` + `ensure_ascii=False`，末尾一个换行）：
同一材料重跑逐字节相同，其 sha256 因此可作证据锚。两者不可逐字节互比（键序与排版不同），
比对一律走 `compare`（按 JSON 解析后逐字段对照）。

载荷验收走宪章 §〇：空件 / 短件（`contentLength` 与实际字节不等）/ 畸形 JSON / 非对象
一律拒收并带原因串；拒收件不进结算，计入 `meta.pins_rejected` 与各池 `rejected`。
**绝不用显示层 `contentSummary` 顶替字节**：只有在 pin 对象本身不含字节（如引擎夹具）时
才回退，且回退路径同样过 §〇 校验，并在 `payload_channel` 里注明。

## 3. 实现位置

| 文件 | 职责 |
|---|---|
| `types.go` | 报数结构（Aggregate / PoolReport / NodeSettlement / Vote / Enumeration / Meta） |
| `payload.go` | §〇 载荷验收（`acceptPayload`）、base64 解码、引擎口径的存在性判定 |
| `settle.go` | v1.2.0 结算（claim/submission/verify + guard） |
| `aggregate.go` | 三池读数组装 + 兼容字段归一 + 费用/时间戳元数据 |
| `enumerate.go` | 游标走查（HTTP 与冻结快照两种 Lister）、载荷装载 |
| `engine_parity_test.go` | 跑引擎自带 9 条夹具 + 夹具 sha256 钉住 + 非空洞性检查 |
| `vectors_test.go` | 本包自测向量（载荷/枚举/结算） |

读取路径有两种 Lister，同一套走查与结算代码：

- 线上：`--manapi https://manapi.metaid.io`（列页 + 逐 pin `content` 端点取字节）
- 冻结：`--snapshot <dir>`（重放已记录页与已落盘字节，网络零依赖、可复现）

## 4. 复现命令

```bash
# 0) 抓冻结快照（七池 cursor 翻页到显式空页 + 逐 pin 原始字节）并产出引擎事件目录
python3 internal/metatask/scripts/freeze_pools.py --out /tmp/mt-snapshot --events /tmp/mt-events

# 自测向量（Go）
CGO_ENABLED=0 go test ./internal/metatask/ -v

# 自测向量（Python 独立实现）
python3 internal/metatask/scripts/recompute_pools.py vectors

# 线上读数
CGO_ENABLED=0 go run ./cmd/metaso-p2p-metatask-pools \
  --manapi https://manapi.metaid.io --root <task_root_pinId> --now <unix>

# 冻结快照复算（脚本侧）+ 与引擎逐项对照
python3 internal/metatask/scripts/recompute_pools.py recompute --snapshot <dir> --root <pinId> --now <unix> --out mine.json
python3 internal/metatask/scripts/recompute_pools.py compare --a engine.json --b mine.json

# 已知节点期望值核对（t35 / t47 / t55；字段缺失与值不同分开报，缺失即 FAIL）
python3 internal/metatask/scripts/recompute_pools.py expect --report mine.json

# 引擎侧对照读数（vendored v1.2.0）
python3 internal/metatask/testdata/engine-fixtures/metatask_replay.v1.2.0.py \\
  --events /tmp/mt-events --root <pinId>
```

## 4.1 依赖登记

本步**未新增任何依赖**，因此无外部厂商 API 引入：

| 依赖 | 来源 | 说明 |
|---|---|---|
| Go 标准库（net/http、encoding/json、crypto/sha256…） | go1.26 自带 | 读取路径与结算全部自写，未引入新 module |
| Python 标准库（argparse、json、hashlib、urllib.parse…） | 系统 python3 | 脚本零第三方包 |
| 系统 `curl` | 操作系统自带 | 仅 `freeze_pools.py` 取数用（本机 Python 缺 CA 证书时的既有通道选择） |
| MANAPI（`https://manapi.metaid.io`） | 仓库既有配置项 `MANAPIBaseURL` | 非新引入；本步未新增任何外部端点 |

## 5. 已知未覆盖项（诚实留白）

1. **真实交易手续费不可得**：索引器 pin 对象只带 `genesisFee`（本次观测全为 0）与
   `outputValue`，没有交易费字段，故本读数只能报这两个字段的和，**不能**回答「这次
   写入花了多少 sats」。要拿真手续费需另行取原始交易（本步未做）。
2. **`total` 语义未判定**：列表接口的 `total` 与实枚举条数不等（claim 105 vs 117、
   verify 237 vs 247），差额原因未查明；因此完成判据**不采用** `total`，只看游标与空页。
3. **`status` 字段未过滤**：verify 池 247 枚里 48 枚 `status=1`（claim 池全为 0），
   语义未判定，本读数只记录分布、不据此过滤（引擎也不过滤，故不影响一致性）。
4. **HTTP 路由未接**：本步交付包 + 命令行读数，尚未注册到 `/api` 路由（未改
   `main.go` 与 config）。「任何 bot 直接用」需要后续接线。
5. **同 owner 验证**：本步为节点级实现步，跨 owner 复核尚未进行。
