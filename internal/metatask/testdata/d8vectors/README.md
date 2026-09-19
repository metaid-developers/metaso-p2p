# d8vectors — skill v1.2.2 判别向量（原样移植）

来源：`metatask-replay` skill v1.2.2 的 `fixtures/` 目录，逐字节复制（未重排）。
skill 自跑证据：`RUN_LOG.txt`（skill runner `fixtures/run_fixtures.py`，2026-09-18 实测 **11/11 PASS**）。

Go 侧对表：`d8_test.go::TestD8VectorsAgainstSkillFixtures` 用同一批事件跑本仓冻结引擎
（`replay.go`），按 guard 口径映射（open→allow；submitted→claimed；claimed→claimed；
verified→verified），断言 `expect.json` 的 verdict/reason，11/11。

| 向量 | 判别点 | 期望（guard 视图） |
|---|---|---|
| v1_open | 无认领 → open | allow / null |
| v2_claimed | 认领持有、窗口内 | reject / claim-rejected:t1:claimed |
| v3_verified | pass 凑满 quorum | reject / claim-rejected:t1:verified |
| v4_ttl_expired | 49h > 48h TTL（恰边界外侧，严格大于） | allow / null |
| v5_released | release 撤销 → 回 open | allow / null |
| v6_missing_failreason | ≥H_ACT fail 缺 failreason → 票无效不拒 | reject / claim-rejected:t1:verified |
| v7_with_failreason | ≥H_ACT fail 带 failreason → 打回 open | allow / null |
| v8_missing_semantic_check | ≥H_ACT pass 缺 semantic_check → 不计权 | reject / claim-rejected:t1:claimed |
| v9_with_semantic_check | ≥H_ACT pass 带 semantic_check → 正常计权 | reject / claim-rejected:t1:verified |
| v10_below_h0_exempt | @189999 = H_ACT−1，缺 sc 票照常计入 | reject / claim-rejected:t1:verified |
| v11_at_h0_gated | @190000 = H_ACT，同形票无效 | reject / claim-rejected:t1:claimed |

每目录 7 个 `<pool>.json`（pin 数组，payload 内联于 contentSummary）+ `expect.json`
（`now` / `expect_verdict` / `expect_reason`）。
