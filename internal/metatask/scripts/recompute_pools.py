#!/usr/bin/env python3
"""recompute_pools.py — metaTask 三池聚合的可复算工具（零第三方依赖）。

子命令
  recompute  从冻结快照（pages/ + content/）独立复算：游标翻页到空页、§〇 载荷
             验收、v1.2.0 结算、三池读数、费用与时间戳元数据，输出 JSON。
             stdout / --out 一律 canonical JSON（sort_keys + 紧凑分隔 + ensure_ascii=False），
             同一材料重跑逐字节相同。
  compare    逐项对照两份聚合结果（引擎 / 本实现），输出对照表 + 差异清单；
             0 差异退出码 0，有差异退出码 1。
  vectors    执行 testdata/metatask-pools-vectors.v1.json 的自测向量（含 N1 截断副本）。
  expect     用 testdata/known-nodes.v1.json 的已知节点期望值核对一份 recompute 读数。

两口径（semantic_check 过滤）
  结算默认走**实现口径**：照 replay v1.2.0 的 #9，缺 `semantic_check` 的票不计票。
  该过滤**不是 spec 条款**——已由裁定件（RULING_PIN）与澄清件（RULING_CLARIFICATION_PIN）
  定为 replay 的实现选择，并要求判决记录并列两套读数：
    (a) 实现口径 = 含该过滤（当前行为）；
    (b) 裁定口径 = 不含该过滤，仅因缺该字段被扣的票恢复计入（其余计票条件满足时）。
  开关：
    --no-semantic-check-filter  单口径输出该 report 的裁定口径读数。
    --dual-report               同时输出两套计数 + 受影响票清单 + 差异汇总。

用法
  python3 recompute_pools.py recompute --snapshot <dir> --root <pinId> --now <unix> --out <file>
  python3 recompute_pools.py recompute --snapshot <dir> --root <pinId> --now <unix> \
      --dual-report [--out <file>]
  python3 recompute_pools.py compare --a <engine.json> --b <mine.json> [--label-a X --label-b Y]
  python3 recompute_pools.py vectors --vectors <file>
  python3 recompute_pools.py expect --report <mine.json> [--nodes <file>]

说明：本脚本是 Python 独立实现（与 Go 实现互为对照），链上材料只读，不写链。
"""
import argparse
import collections
import hashlib
import json
import os
import sys

PATHS = ["task", "tree", "spec", "claim", "release", "submission", "verify"]
POOL = {"claim": "claim", "submission": "submission", "verify": "verify"}
VOTE_FILTER_NOTE = "#9 缺 semantic_check 不计票；#8 fail 缺 failreason 按 invalid"

# 两口径：semantic_check 过滤是实现选择而非 spec 条款。
CALIBER_IMPL = "implementation"
CALIBER_RULING = "ruling"
RULING_PIN = "1bf3cb15470a45655fc3e2a52f1a309b04a8f8fd86989243f9f964eb39d7988ci0"
RULING_CLARIFICATION_PIN = "6adaa70088f0fb136eda7a5f314ac61c88830b8ba63ece72d8b5091cc0e07af8i0"
RULING_NOTE = (
    "#9 的「缺 semantic_check 不计票」不是 spec 条款（spec v1.0→v1.2.11 从未写入该字段），"
    "来源是 metatask-replay 的实现自述；裁定：不追溯、不作 spec 级计票规则，"
    "凡仅因缺该字段被扣的票恢复计入（其余计票条件满足时）。"
    "#8 的「fail 缺 failreason 按 invalid」与本条无关，两口径一致保留。"
)
# 受影响票清单里逐票投影的节点计数字段（两口径并排比较用）。
NODE_COUNT_FIELDS = ("node", "status", "effective_status", "effective_pass", "valid_pass_total",
                     "valid_fail_total", "pass_raw", "fail_raw", "invalid_votes", "voter_bots")


def sha256_bytes(b):
    return hashlib.sha256(b).hexdigest()


def sha256_file(p):
    with open(p, "rb") as f:
        return hashlib.sha256(f.read()).hexdigest()


def canonical_json(obj):
    """Canonical JSON: sorted keys, compact separators, no ASCII escaping.

    Identical input yields identical bytes, so two runs of the recompute are
    byte-comparable and their sha256 is a valid evidence anchor. This is the
    only form the recompute writes to stdout / --out.
    """
    return json.dumps(obj, ensure_ascii=False, sort_keys=True, separators=(",", ":"))


# ---------------------------------------------------------------- §〇 载荷验收

def accept_payload(raw, declared_length, channel):
    """宪章 §〇 验收：空件/短件/畸形/非对象一律拒收并给出原因串。"""
    if not raw:
        return "empty", None, "payload bytes empty (channel=%s)" % channel
    if declared_length and len(raw) != declared_length:
        return ("short_item", None,
                "short item: declared contentLength=%d actual bytes=%d (channel=%s)"
                % (declared_length, len(raw), channel))
    try:
        obj = json.loads(raw.decode("utf-8"))
    except Exception as e:
        return "malformed_json", None, "payload does not parse as JSON (%s)" % e
    if not isinstance(obj, dict):
        return "not_object", None, "payload parses as %s, not a JSON object" % type(obj).__name__
    return "ok", obj, ""


def present(v):
    """引擎口径的存在性判定 = Python str(v or '').strip() != ''（非字符串非空值也算存在）。"""
    if v is None or v is False or v == "" or v == 0 or v == [] or v == {}:
        return False
    return bool(str(v).strip())


def vote_invalid(b, semantic_check_filter=True):
    """票无效判据；两口径共用，只差 semantic_check 一项（见 RULING_NOTE）。

    semantic_check_filter=True  → 实现口径：缺 `semantic_check` 即不计票。
    semantic_check_filter=False → 裁定口径：不因缺该字段扣票；#8 的 failreason 判据不变。
    """
    if semantic_check_filter and not present(b.get("semantic_check")):
        return "missing_semantic_check"
    if b.get("verdict") == "fail" and not present(b.get("failreason")):
        return "missing_failreason"
    return None


# ---------------------------------------------------------------- 枚举（游标到空页）

def enumerate_snapshot(snapshot, size=50):
    """按冻结快照重放七池抓取：cursor 逐页前进，只在显式空页收口。"""
    walk = {"size": size, "paths": {}, "complete": True, "unresolved": []}
    pins_by_path = {}
    for path in PATHS:
        pool = path
        pdir = os.path.join(snapshot, "pages", pool)
        pages = sorted(
            [f for f in os.listdir(pdir) if f.endswith(".json")],
            key=lambda x: int(x[:-5])) if os.path.isdir(pdir) else []
        # 重建 cursor_in -> page 的映射（与抓取时一致）
        by_cursor = {}
        cursor_in = ""
        for f in pages:
            raw = open(os.path.join(pdir, f), "rb").read()
            doc = json.loads(raw.decode("utf-8"))
            by_cursor[cursor_in] = (f, raw, doc)
            data = doc.get("data") or {}
            cursor_in = data.get("nextCursor") or ""
        pins, seen, order = [], set(), []
        report = {"protocol_path": "/protocols/metatask/" + pool, "pages": 0, "records": 0,
                  "unique_records": 0, "duplicates_dropped": 0, "cursor_exhausted": False,
                  "complete": False, "page_hashes": [], "page_sizes": [], "reported_totals": []}
        cursor = ""
        while True:
            if cursor not in by_cursor:
                report["termination"] = "snapshot_walk_off_record"
                break
            fname, raw, doc = by_cursor[cursor]
            data = doc.get("data")
            report["pages"] += 1
            report["page_hashes"].append(sha256_bytes(raw))
            if data is None or "list" not in data:
                report["termination"] = "list_field_missing"
                break
            lst, nxt = data.get("list"), (data.get("nextCursor") or "")
            report["page_sizes"].append(0 if lst is None else len(lst))
            report["reported_totals"].append(data.get("total"))
            for item in (lst or []):
                report["records"] += 1
                if item["id"] in seen:
                    report["duplicates_dropped"] += 1
                    continue
                seen.add(item["id"])
                pins.append(item)
                order.append(item["id"])
            if lst is None and not nxt:
                report.update(termination="list_null_empty_cursor", cursor_exhausted=True, complete=True)
                break
            if lst is None:
                report["termination"] = "list_null_with_cursor"
                break
            if len(lst) == 0 and not nxt:
                report.update(termination="empty_page_cursor_exhausted", cursor_exhausted=True, complete=True)
                break
            if len(lst) == 0:
                report["termination"] = "empty_page_with_cursor"
                break
            if not nxt:
                report["termination"] = "full_page_no_cursor"
                break
            cursor = nxt
        report["unique_records"] = len(pins)
        walk["paths"][report["protocol_path"]] = report
        if not report["complete"]:
            walk["complete"] = False
            walk["unresolved"].append(report["protocol_path"])
        pins_by_path[path] = pins
    all_ids = sorted(p["id"] for path in PATHS for p in pins_by_path[path])
    walk["pin_set_sha256"] = sha256_bytes("\n".join(all_ids).encode())
    return pins_by_path, walk


def load_payloads(snapshot, pins_by_path):
    """逐 pin 取原始字节并做 §〇 验收；拒收件记录原因，不进结算。"""
    verdicts = {}
    for path in PATHS:
        for p in pins_by_path[path]:
            cpath = os.path.join(snapshot, "content", p["id"] + ".bin")
            raw = open(cpath, "rb").read() if os.path.exists(cpath) else b""
            state, obj, reason = accept_payload(raw, p.get("contentLength") or 0, "content")
            p["_payload"] = obj
            p["_state"] = state
            p["_reason"] = reason
            p["_bytes"] = raw
            verdicts[(path, p["id"])] = state
    return verdicts


def okey(p):
    h = p.get("genesisHeight", -1)
    h = (1 << 63) - 1 if h is None or h < 0 else h
    tx = p.get("txIndex") or 0
    ts = p.get("seenTime") or p.get("timestamp") or 0
    return (h, tx, ts, p.get("id", ""))


def ok_pins(pins):
    return sorted([p for p in pins if p["_state"] == "ok"], key=okey)


# ---------------------------------------------------------------- 结算（v1.2.0）

def settle(pins_by_path, root, now, semantic_check_filter=True):
    """v1.2.0 结算。semantic_check_filter 选口径（默认实现口径，见 RULING_NOTE）。"""
    claims = ok_pins(pins_by_path["claim"])
    releases = ok_pins(pins_by_path["release"])
    subs_pins = ok_pins(pins_by_path["submission"])
    verifies = ok_pins(pins_by_path["verify"])
    trees = ok_pins(pins_by_path["tree"])

    root_pin = next((p for p in ok_pins(pins_by_path["task"]) if p["id"] == root), None)
    if root_pin is None:
        raise SystemExit("task root %s not found" % root)
    task = root_pin["_payload"]
    policy = task.get("policy") or {}
    quorum = int(policy.get("verify_quorum", 2))
    ttl_h = int(policy.get("claim_ttl_hours", 0) or 0)
    win_h = int(policy.get("verify_window_hours", 0) or 0)
    root_author = root_pin.get("globalMetaId")
    tree_nodes = []
    treeid = task.get("treeid")
    if treeid:
        for tp in trees:
            if tp["id"] == treeid:
                tree_nodes = [str(n["id"]) for n in (tp["_payload"].get("nodes") or []) if "id" in n]
                break

    # §4.1 claim/release 全局混排
    holders, timeline = {}, []
    for p in claims:
        if p["_payload"].get("taskid") == root:
            timeline.append(p)
    for p in releases:
        if p["_payload"].get("taskid") == root:
            timeline.append(p)
    timeline.sort(key=okey)
    for p in timeline:
        body = p["_payload"]
        node = str(body.get("node"))
        if p["path"].endswith("/claim"):
            if node not in holders:
                holders[node] = {"pin": p["id"], "claimant": p.get("globalMetaId"),
                                 "t0": p.get("timestamp") or p.get("seenTime") or 0}
        else:
            if holders.get(node, {}).get("pin") == body.get("claimid"):
                del holders[node]

    # §4.6 submission
    subs, sub_records = {}, collections.defaultdict(list)
    for p in subs_pins:
        body = p["_payload"]
        if body.get("taskid") != root:
            continue
        node, cid = str(body.get("node")), body.get("claimid")
        sub_records[node].append(p)
        h = holders.get(node)
        if not h or h["pin"] != cid:
            continue
        if node not in subs:
            subs[node] = {"pin": p["id"], "author": p.get("globalMetaId"), "claim": cid,
                          "t": p.get("timestamp") or p.get("seenTime") or 0}

    # 票：v1.2.0 先过滤无效票，再每 (target,bot) 取最后一票（池级）
    last = {}
    for p in verifies:
        b = p["_payload"]
        tgt, bot = b.get("targetid"), p.get("globalMetaId")
        if tgt and bot and not vote_invalid(b, semantic_check_filter):
            last[(tgt, bot)] = b

    def votes_for(target):
        valid, invalid, pr, fr = [], [], 0, 0
        for p in verifies:
            b = p["_payload"]
            if b.get("targetid") != target:
                continue
            pr += 1 if b.get("verdict") == "pass" else 0
            fr += 1 if b.get("verdict") == "fail" else 0
            reason = vote_invalid(b, semantic_check_filter)
            rec = {"pin": p["id"], "bot": p.get("globalMetaId"), "verdict": b.get("verdict"),
                   "valid": reason is None, "invalid_reason": reason,
                   "semantic_check_type": type(b.get("semantic_check")).__name__}
            (valid if reason is None else invalid).append(rec)
        return valid, invalid, pr, fr

    def guard(node):
        holder, inflight, hs = None, False, []
        ntl = [p for p in claims if p["_payload"].get("taskid") == root and str(p["_payload"].get("node")) == node]
        ntl += [p for p in releases if p["_payload"].get("taskid") == root and str(p["_payload"].get("node")) == node]
        for p in sorted(ntl, key=okey):
            if p["path"].endswith("/claim"):
                if holder is None:
                    holder = {"pin": p["id"], "claimant": p.get("globalMetaId"),
                              "t0": p.get("timestamp") or p.get("seenTime") or 0}
                    if (p.get("genesisHeight", -1) or -1) < 0:
                        inflight = True
            elif holder and holder["pin"] == p["_payload"].get("claimid"):
                holder = None
        sub = None
        if holder:
            for p in subs_pins:
                b = p["_payload"]
                if b.get("taskid") == root and str(b.get("node")) == node and b.get("claimid") == holder["pin"]:
                    sub = {"pin": p["id"], "author": p.get("globalMetaId"),
                           "t": p.get("timestamp") or p.get("seenTime") or 0}
                    break
        good = bad = 0
        if sub:
            lv = {}
            for p in verifies:
                b = p["_payload"]
                if b.get("targetid") != sub["pin"] or vote_invalid(b, semantic_check_filter):
                    continue
                if p.get("globalMetaId"):
                    lv[p["globalMetaId"]] = b
            for bot, b in lv.items():
                if b.get("verdict") == "pass" and bot not in (sub["author"], root_author):
                    good += 1
                elif b.get("verdict") == "fail":
                    bad += 1
        if inflight and holder:
            state = "claimed"
        elif sub and good >= quorum and not bad:
            state = "verified"
        elif sub and bad:
            state = "open"
        elif holder and not sub and ttl_h and (now - int(holder["t0"])) > ttl_h * 3600:
            state = "open"
        elif sub and win_h and (now - int(sub["t"])) > win_h * 3600 and good < quorum:
            state = "open"
        elif holder:
            state = "claimed"
        else:
            state = "open"
        return state, (None if state == "open" else "claim-rejected:%s:%s" % (node, state)), holder, sub

    nodes = set(tree_nodes) | set(holders) | set(subs)

    def verified_sub(node):
        s = subs.get(node)
        if not s:
            return None, 0, 0
        good = bad = 0
        lv = {}
        for p in verifies:
            b = p["_payload"]
            if b.get("targetid") != s["pin"] or vote_invalid(b, semantic_check_filter):
                continue
            if p.get("globalMetaId"):
                lv[p["globalMetaId"]] = b
        for bot, b in lv.items():
            if b.get("verdict") == "pass" and bot not in (s["author"], root_author):
                good += 1
            elif b.get("verdict") == "fail":
                bad += 1
        return s, good, bad

    out_nodes, rep = [], collections.Counter()
    for node in sorted(nodes, key=lambda x: (len(x), x)):
        s, good, bad = verified_sub(node)
        h = holders.get(node)
        verified = s is not None and good >= quorum and bad == 0
        status = "verified" if verified else ("claimed" if h else "open")
        if verified:
            rep[s["author"]] += 1
        est, ereason, gh, gsub = guard(node)
        rec = {
            "node": node, "status": status, "effective_status": est,
            "effective_reason": ereason, "holder": (h or {}).get("claimant"),
            "holder_pin": (h or {}).get("pin"), "submission": (s or {}).get("pin"),
            "submission_author": (s or {}).get("author"), "submission_claimid": (s or {}).get("claim"),
            "submission_records": len(sub_records.get(node, [])),
            "effective_pass": good if verified else 0,
            "valid_pass_total": 0, "valid_fail_total": 0, "pass_raw": 0, "fail_raw": 0,
            "invalid_votes": 0, "voter_bots": 0,
        }
        if rec["submission"]:
            valid, invalid, pr, fr = votes_for(rec["submission"])
            lv = {}
            for v in valid:
                lv[v["bot"]] = v
            rec["valid_pass_total"] = sum(
                1 for bot, v in lv.items()
                if v["verdict"] == "pass" and bot not in (rec["submission_author"], root_author))
            rec["valid_fail_total"] = sum(1 for v in lv.values() if v["verdict"] == "fail")
            rec["pass_raw"], rec["fail_raw"] = pr, fr
            rec["invalid_votes"] = len(invalid)
            rec["voter_bots"] = len({v["bot"] for v in valid + invalid})
        out_nodes.append(rec)

    effective_subs = {s["pin"] for s in subs.values()}
    in_scope_subs = {p["id"] for p in subs_pins if p["_payload"].get("taskid") == root}
    verify_votes_task = sum(1 for (t, b) in last if t in effective_subs)

    def pool_meta(pins):
        ts = [p.get("timestamp") or 0 for p in pins if (p.get("timestamp") or 0)]
        sn = [p.get("seenTime") or 0 for p in pins if (p.get("seenTime") or 0)]
        return {
            "first_timestamp": min(ts) if ts else 0, "last_timestamp": max(ts) if ts else 0,
            "first_seen_time": min(sn) if sn else 0, "last_seen_time": max(sn) if sn else 0,
            "genesis_fee_sum": sum(p.get("genesisFee") or 0 for p in pins),
            "output_value_sum": sum(p.get("outputValue") or 0 for p in pins),
            "confirmed": sum(1 for p in pins if (p.get("genesisHeight", -1) or -1) >= 0),
            "mempool": sum(1 for p in pins if (p.get("genesisHeight", -1) or -1) < 0),
        }

    in_scope_claims = [p for p in claims if p["_payload"].get("taskid") == root]
    in_scope_sub_pins = [p for p in subs_pins if p["_payload"].get("taskid") == root]
    in_scope_verifies = [p for p in verifies if p["_payload"].get("targetid") in in_scope_subs]
    pools = {
        "claim": dict({"pool": "claim", "protocol_path": "/protocols/metatask/claim",
                       "records": len(claims), "records_in_scope": len(in_scope_claims),
                       "effective": len(holders),
                       "ignored": max(0, len(in_scope_claims) - len(holders)),
                       "participants": len({p.get("globalMetaId") for p in in_scope_claims}),
                       "rejected": sum(1 for p in pins_by_path["claim"] if p["_state"] != "ok")},
                      **pool_meta(in_scope_claims)),
        "submission": dict({"pool": "submission", "protocol_path": "/protocols/metatask/submission",
                            "records": len(subs_pins), "records_in_scope": len(in_scope_sub_pins),
                            "effective": len(subs),
                            "ignored": max(0, len(in_scope_sub_pins) - len(subs)),
                            "participants": len({p.get("globalMetaId") for p in in_scope_sub_pins}),
                            "rejected": sum(1 for p in pins_by_path["submission"] if p["_state"] != "ok")},
                           **pool_meta(in_scope_sub_pins)),
        "verify": dict({"pool": "verify", "protocol_path": "/protocols/metatask/verify",
                        "records": len(verifies), "records_in_scope": len(in_scope_verifies),
                        "effective": verify_votes_task,
                        "ignored": max(0, len(in_scope_verifies) - verify_votes_task),
                        "participants": len({p.get("globalMetaId") for p in in_scope_verifies}),
                        "bots_voting": len({p.get("globalMetaId") for p in in_scope_verifies}),
                        "invalid_votes": sum(1 for p in in_scope_verifies
                                             if vote_invalid(p["_payload"], semantic_check_filter)),
                        "rejected": sum(1 for p in pins_by_path["verify"] if p["_state"] != "ok")},
                       **pool_meta(in_scope_verifies)),
    }
    for pool in ("claim", "submission", "verify"):
        path = pool
        recs = [{"pin": p["id"], "state": p["_state"], "reason": p["_reason"], "channel": "content"}
                for p in pins_by_path[path] if p["_state"] != "ok"]
        if recs:
            pools[pool]["rejected_records"] = recs
    return {
        "task_root": root, "task_title": task.get("title"), "quorum": quorum, "policy": policy,
        "task_author": root_author, "tree_id": treeid, "tree_nodes": len(tree_nodes),
        "all_verified": len(out_nodes) > 0 and all(n["status"] == "verified" for n in out_nodes),
        "nodes": out_nodes, "reputation": dict(rep), "verify_votes": len(last),
        "verify_votes_task_scoped": verify_votes_task, "pools": pools,
        "meta": {
            "fee_fields": ["genesisFee", "outputValue"],
            "genesis_fee_sum": sum(p.get("genesisFee") or 0 for path in PATHS for p in pins_by_path[path]),
            "output_value_sum": sum(p.get("outputValue") or 0 for path in PATHS for p in pins_by_path[path]),
            "fee_note": "indexer pin object carries no transaction-fee field",
            "pins_enumerated": sum(len(pins_by_path[path]) for path in PATHS),
            "pins_rejected": sum(1 for path in PATHS for p in pins_by_path[path] if p["_state"] != "ok"),
        },
    }


def cmd_recompute(args):
    pins_by_path, walk = enumerate_snapshot(args.snapshot)
    load_payloads(args.snapshot, pins_by_path)
    rep = settle(pins_by_path, args.root, args.now,
                 semantic_check_filter=not args.no_semantic_check_filter)
    rep["enumeration"] = walk

    if args.dual_report:
        # 两套读数同出：实现口径（含该过滤）与裁定口径（不含）。
        rep_ruling = settle(pins_by_path, args.root, args.now, semantic_check_filter=False)
        report = build_dual_report(pins_by_path, rep, rep_ruling, walk, args.snapshot,
                                   args.root, args.now)
        blob = canonical_json(report)
        # 清单的 stdout 必须是**纯 canonical JSON**，sha256 走 stderr 与 sidecar。
        if args.out:
            data = (blob + "\n").encode("utf-8")
            with open(args.out, "wb") as f:
                f.write(data)
            digest = sha256_bytes(data)
            with open(args.out + ".sha256", "w", encoding="utf-8") as f:
                f.write("%s  %s\n" % (digest, os.path.basename(args.out)))
            print("dual-report sha256=%s (canonical, %d bytes, %s)"
                  % (digest, len(data), args.out), file=sys.stderr)
        print(blob)
    else:
        blob = canonical_json(rep)
        if args.out:
            open(args.out, "w").write(blob + "\n")
            print("wrote %s" % args.out)
        else:
            print(blob)
    if not walk["complete"] and not args.allow_incomplete:
        print("INCOMPLETE ENUMERATION: %s" % walk["unresolved"], file=sys.stderr)
        return 2
    return 0


# ---------------------------------------------------------------- 两口径对照

def caliber_view(rep):
    """把一份 settle 报告投影成「计数视图」：标量 + 逐节点计数字段。

    两口径的并排读数就是两份 caliber_view；节点身份（holder/submission 等）不随计票
    口径变化，只投影计数字段，避免同一事实写两遍。
    """
    return {
        "quorum": rep["quorum"],
        "all_verified": rep["all_verified"],
        "verify_votes": rep["verify_votes"],
        "verify_votes_task_scoped": rep["verify_votes_task_scoped"],
        "reputation": rep["reputation"],
        "verify_pool": {k: rep["pools"]["verify"].get(k) for k in (
            "records", "records_in_scope", "effective", "ignored", "participants",
            "bots_voting", "invalid_votes")},
        "nodes": [{k: n.get(k) for k in NODE_COUNT_FIELDS} for n in rep["nodes"]],
    }


def ballot_inventory(pins_by_path, rep_impl, rep_ruling):
    """逐票列出受影响票清单：票 pin / 原判定 / 是否因缺 semantic_check 被扣 /
    裁定口径下的判定 / 票所属 target（submission pin）。

    范围 = 本任务根的全部 submission 票（含**未生效**的 submission：其 claim 无效或被
    release 过，票不进节点计数，但仍逐票列出并标 target_effective=false），否则清单与
    池读数（records_in_scope）对不上。

    「受影响」= 两口径下该票的无效判据不同。效果分三档：
      restored_to_valid       仅缺 semantic_check → 裁定口径下恢复为有效票；
      reason_only             同时缺 failreason → 两口径都无效，但扣票理由改变；
      none                    两口径判定一致。
    """
    ident = {}
    for n in rep_impl["nodes"]:
        if n.get("submission"):
            ident[n["submission"]] = {"node": n["node"], "author": n.get("submission_author"),
                                      "effective": True}
    for p in ok_pins(pins_by_path["submission"]):
        b = p["_payload"]
        if b.get("taskid") != rep_impl["task_root"] or p["id"] in ident:
            continue
        ident[p["id"]] = {"node": str(b.get("node")), "author": p.get("globalMetaId"),
                          "effective": False}
    root_author = rep_impl["task_author"]

    rows, summary = [], collections.Counter()
    for p in ok_pins(pins_by_path["verify"]):
        b = p["_payload"]
        target = b.get("targetid")
        summary["verify_pins_total"] += 1
        if target not in ident:
            summary["ballots_out_of_scope"] += 1
            continue
        tgt = ident[target]
        node, sub_author, effective = tgt["node"], tgt["author"], tgt["effective"]
        bot = p.get("globalMetaId")
        verdict = b.get("verdict")
        reason_impl = vote_invalid(b, True)
        reason_ruling = vote_invalid(b, False)
        if reason_impl == reason_ruling:
            effect = "none"
        elif reason_impl == "missing_semantic_check" and reason_ruling is None:
            effect = "restored_to_valid"
        elif reason_impl == "missing_semantic_check" and reason_ruling == "missing_failreason":
            effect = "reason_only"
        else:
            effect = "other"
        independent = bot not in (sub_author, root_author)
        row = {
            "pin": p["id"],
            "target": target,
            "target_node": node,
            "target_author": sub_author,
            "target_effective": effective,
            "verifier": bot,
            "verdict_raw": verdict,
            "verifier_owner": b.get("verifier_owner") or None,
            "semantic_check_present": present(b.get("semantic_check")),
            "original_verdict": verdict if reason_impl is None else "invalid",
            "invalid_reason_impl": reason_impl,
            "deducted_for_missing_semantic_check": reason_impl == "missing_semantic_check",
            "ruling_verdict": verdict if reason_ruling is None else "invalid",
            "invalid_reason_ruling": reason_ruling,
            "effect": effect,
            "affected": effect != "none",
            "counts_pass_impl": effective and reason_impl is None and verdict == "pass" and independent,
            "counts_pass_ruling": effective and reason_ruling is None and verdict == "pass" and independent,
            "counts_fail_impl": effective and reason_impl is None and verdict == "fail",
            "counts_fail_ruling": effective and reason_ruling is None and verdict == "fail",
        }
        rows.append(row)
        summary["ballots_in_scope"] += 1
        if not effective:
            summary["ballots_target_not_effective"] += 1
        if row["affected"]:
            summary["affected"] += 1
        summary["effect:" + effect] += 1
        summary["original:" + row["original_verdict"]] += 1
        summary["ruling:" + row["ruling_verdict"]] += 1
        summary["counts_pass_impl"] += 1 if row["counts_pass_impl"] else 0
        summary["counts_pass_ruling"] += 1 if row["counts_pass_ruling"] else 0
    return rows, {k: v for k, v in sorted(summary.items())}


def node_diff(rep_impl, rep_ruling):
    """哪些节点票数变化、哪些节点 verified 状态变化。"""
    a = {n["node"]: n for n in rep_impl["nodes"]}
    b = {n["node"]: n for n in rep_ruling["nodes"]}
    changed, counts_changed, status_changed = [], [], []
    for node in sorted(set(a) | set(b), key=lambda x: (len(x), x)):
        na, nb = a.get(node, {}), b.get(node, {})
        deltas = {k: [na.get(k), nb.get(k)] for k in NODE_COUNT_FIELDS if na.get(k) != nb.get(k)}
        if not deltas:
            continue
        rec = {"node": node, "deltas": deltas}
        changed.append(rec)
        if "status" in deltas:
            status_changed.append(rec)
        if any(k != "status" for k in deltas):
            counts_changed.append(rec)
    scalars = [{"field": k, "implementation": rep_impl.get(k), "ruling": rep_ruling.get(k)}
               for k in ("all_verified", "verify_votes", "verify_votes_task_scoped", "reputation")
               if rep_impl.get(k) != rep_ruling.get(k)]
    scalars.append({"field": "pools.verify.invalid_votes",
                    "implementation": rep_impl["pools"]["verify"]["invalid_votes"],
                    "ruling": rep_ruling["pools"]["verify"]["invalid_votes"]})
    return {
        "nodes_compared": len(set(a) | set(b)),
        "nodes_changed": len(changed),
        "nodes_unchanged": len(set(a) | set(b)) - len(changed),
        "scalars_changed": scalars,
        "counts_changed": counts_changed,
        "status_changed": status_changed,
    }


def build_dual_report(pins_by_path, rep_impl, rep_ruling, walk, snapshot, root, now):
    rows, bsum = ballot_inventory(pins_by_path, rep_impl, rep_ruling)
    diff = node_diff(rep_impl, rep_ruling)
    known = {}
    for node in ("t35", "t47", "t55"):
        ia = next((n for n in rep_impl["nodes"] if n["node"] == node), None)
        ib = next((n for n in rep_ruling["nodes"] if n["node"] == node), None)
        if ia is None and ib is None:
            continue
        known[node] = {
            "implementation": {k: (ia or {}).get(k) for k in NODE_COUNT_FIELDS},
            "ruling": {k: (ib or {}).get(k) for k in NODE_COUNT_FIELDS},
            "status_changed": (ia or {}).get("status") != (ib or {}).get("status"),
        }
    manifest = os.path.join(snapshot, "manifest.json")
    return {
        "schema": "metatask-dual-caliber-report.v1",
        "task_root": root,
        "now": now,
        "producer": {
            "script": "internal/metatask/scripts/recompute_pools.py",
            "script_sha256": sha256_file(os.path.abspath(__file__)),
            "settlement": "v1.2.0（#9 缺 semantic_check / #8 fail 缺 failreason）",
            "canonical_json": "sort_keys + separators=(',',':') + ensure_ascii=False",
        },
        "ruling": {
            "pin": RULING_PIN,
            "clarification_pin": RULING_CLARIFICATION_PIN,
            "filter_is_spec_clause": False,
            "note": RULING_NOTE,
            "calibers": {CALIBER_IMPL: "含 semantic_check 过滤（replay v1.2.0 实现选择）",
                         CALIBER_RULING: "不含该过滤（协议应有行为）"},
            "restore_condition": "target 有效 ∧ 跨 owner ∧ verdict 明确",
        },
        "material": {
            "snapshot": os.path.abspath(snapshot),
            "snapshot_manifest_sha256": sha256_file(manifest) if os.path.exists(manifest) else None,
            "enumeration": walk,
            "verify_pins_total": len(pins_by_path["verify"]),
        },
        "calibers": {CALIBER_IMPL: caliber_view(rep_impl), CALIBER_RULING: caliber_view(rep_ruling)},
        "ballots": rows,
        "ballot_summary": bsum,
        "diff": diff,
        "known_nodes": known,
    }


# ---------------------------------------------------------------- 逐项对照

def normalize_nodes(doc):
    """接受引擎形态（nodes 为 dict）与本实现形态（nodes 为 list）。"""
    nodes = doc.get("nodes")
    if isinstance(nodes, dict):
        return {k: v for k, v in nodes.items()}
    out = {}
    for n in nodes or []:
        out[n.get("node")] = n
    return out


def cmd_compare(args):
    a, b = json.load(open(args.a)), json.load(open(args.b))
    la, lb = args.label_a, args.label_b
    an, bn = normalize_nodes(a), normalize_nodes(b)
    diffs, rows = [], []
    scalars = [("quorum", a.get("quorum"), b.get("quorum")),
               ("all_verified", a.get("all_verified"), b.get("all_verified")),
               ("verify_votes", a.get("verify_votes"), b.get("verify_votes")),
               ("reputation", a.get("reputation"), b.get("reputation"))]
    for name, va, vb in scalars:
        rows.append(("SCALAR", name, va, vb, "OK" if va == vb else "DIFF"))
        if va != vb:
            diffs.append(("SCALAR", name, va, vb))
    for node in sorted(set(an) | set(bn), key=lambda x: (len(x), x)):
        na, nb = an.get(node), bn.get(node)
        if na is None or nb is None:
            diffs.append((node, "presence", na is not None, nb is not None))
            rows.append((node, "presence", "present" if na else "absent", "present" if nb else "absent", "DIFF"))
            continue
        for field in ("status", "holder", "submission", "effective_pass"):
            va, vb = na.get(field), nb.get(field)
            if va in (None, ""):
                va = None
            if vb in (None, ""):
                vb = None
            ok = va == vb
            rows.append((node, field, va, vb, "OK" if ok else "DIFF"))
            if not ok:
                diffs.append((node, field, va, vb))
    print("| 对象 | 字段 | %s | %s | 判定 |" % (la, lb))
    print("|---|---|---|---|---|")
    for r in rows:
        print("| %s | %s | %s | %s | %s |" % (r[0], r[1], r[2], r[3], r[4]))
    print()
    print("对照节点数：%d（双方并集），差异项：%d" % (len(set(an) | set(bn)), len(diffs)))
    for d in diffs:
        print("  DIFF %s" % (d,))
    summary = {"nodes": len(set(an) | set(bn)), "compared_fields": len(rows), "diffs": len(diffs),
               "diff_detail": diffs, "label_a": la, "label_b": lb,
               "sha256_a": sha256_file(args.a), "sha256_b": sha256_file(args.b)}
    if args.summary:
        open(args.summary, "w").write(json.dumps(summary, ensure_ascii=False, indent=2) + "\n")
    return 0 if not diffs else 1


# ---------------------------------------------------------------- 自测向量

def vector_pins(events):
    """把向量事件装成 settle 的输入（载荷走 §〇 验收，root 取首个 task 事件）。"""
    pins_by_path = {p: [] for p in PATHS}
    root = ""
    for e in events:
        raw = json.dumps(e["payload"]).encode()
        state, obj, _ = accept_payload(raw, len(raw), "vector")
        pins_by_path[e["path"].rsplit("/", 1)[-1]].append(dict(e, _payload=obj, _state=state,
                                                               _reason="", _bytes=raw))
        if e["path"].endswith("/task") and not root:
            root = e["id"]
    return pins_by_path, root


def settle_vector_check(rep, expect):
    """核对一份向量结算结果：字段缺失与值不同都算失败，返回 (坏项, 节点表)。"""
    by = {n["node"]: n for n in rep["nodes"]}
    bad = []
    if rep["quorum"] != expect["quorum"]:
        bad.append("quorum=%s" % rep["quorum"])
    for scalar in ("verify_votes", "all_verified"):
        if scalar in expect and rep[scalar] != expect[scalar]:
            bad.append("%s=%s want %s" % (scalar, rep[scalar], expect[scalar]))
    for node, want in expect["nodes"].items():
        got = by.get(node)
        if got is None:
            bad.append("%s missing" % node)
            continue
        for k, wv in want.items():
            if got.get(k) != wv:
                bad.append("%s.%s=%s want %s" % (node, k, got.get(k), wv))
    return bad, by


def cmd_vectors(args):
    vf = json.load(open(args.vectors))
    passed = failed = 0
    for v in vf["payload_vectors"]:
        state, obj, reason = accept_payload(v["payload"].encode(), v.get("declared_content_length") or 0, v["channel"])
        got = "accept" if state == "ok" else "reject"
        ok = got == v["expect"] and (not v.get("expect_state") or state == v["expect_state"]) \
            and (got == "accept" or reason)
        print("  %-44s %s" % (v["id"], "PASS" if ok else "FAIL(state=%s)" % state))
        passed, failed = (passed + 1, failed) if ok else (passed, failed + 1)
    for v in vf["enumeration_vectors"]:
        pages = []
        cursor_in = ""
        for pv in v["pages"]:
            n = pv.get("list_size")
            page = {"data": {"nextCursor": pv.get("next_cursor", ""), "total": 0},
                    "raw": json.dumps(pv).encode()}
            if pv.get("list_missing"):
                page["data"].pop("list", None)
            elif pv.get("list_null"):
                page["data"]["list"] = None
            else:
                page["data"]["list"] = [{"id": "%s@%s-%d" % (v["id"], cursor_in, i)} for i in range(n or 0)]
            pages.append((cursor_in, page))
            cursor_in = pv.get("next_cursor", "")
        # 就地执行（与 Go 同规则）
        seen, unique = set(), 0
        walk, term, complete, exhausted = 0, None, False, False
        cursor = ""
        by = dict(pages)
        while True:
            if cursor not in by:
                term = "script_exhausted"
                break
            data = by[cursor]["data"]
            walk += 1
            if "list" not in data:
                term = "list_field_missing"
                break
            lst, nxt = data.get("list"), data.get("nextCursor") or ""
            for item in (lst or []):
                if item["id"] not in seen:
                    seen.add(item["id"])
                    unique += 1
            if lst is None and not nxt:
                term, complete, exhausted = "list_null_empty_cursor", True, True
                break
            if lst is None:
                term = "list_null_with_cursor"
                break
            if len(lst) == 0 and not nxt:
                term, complete, exhausted = "empty_page_cursor_exhausted", True, True
                break
            if len(lst) == 0:
                term = "empty_page_with_cursor"
                break
            if not nxt:
                term = "full_page_no_cursor"
                break
            cursor = nxt
        ok = complete == v["expect_complete"] and term == v["expect_termination"] \
            and ("expect_unique" not in v or unique == v["expect_unique"])
        print("  %-44s %s" % (v["id"], "PASS" if ok else "FAIL(term=%s complete=%s unique=%d)" % (term, complete, unique)))
        passed, failed = (passed + 1, failed) if ok else (passed, failed + 1)
    for v in vf["settlement_vectors"]:
        pins_by_path, root = vector_pins(v["events"])
        rep = settle(pins_by_path, root, 20000)
        bad, _ = settle_vector_check(rep, v["expect"])
        ok = not bad
        print("  %-44s %s%s" % (v["id"], "PASS" if ok else "FAIL", "" if ok else " " + "; ".join(bad)))
        passed, failed = (passed + 1, failed) if ok else (passed, failed + 1)
    for v in vf.get("caliber_vectors", []):
        pins_by_path, root = vector_pins(v["events"])
        rep_impl = settle(pins_by_path, root, 20000, semantic_check_filter=True)
        rep_ruling = settle(pins_by_path, root, 20000, semantic_check_filter=False)
        bad = ["%s:%s" % (CALIBER_IMPL, x) for x in settle_vector_check(rep_impl, v["expect_impl"])[0]]
        bad += ["%s:%s" % (CALIBER_RULING, x) for x in settle_vector_check(rep_ruling, v["expect_ruling"])[0]]
        rows = {r["pin"]: r for r in ballot_inventory(pins_by_path, rep_impl, rep_ruling)[0]}
        for pin, want in (v.get("expect_ballots") or {}).items():
            got = rows.get(pin)
            if got is None:
                bad.append("ballot %s missing" % pin[:8])
                continue
            for k, wv in want.items():
                if got.get(k) != wv:
                    bad.append("ballot %s.%s=%s want %s" % (pin[:8], k, got.get(k), wv))
        ok = not bad
        print("  %-44s %s%s" % (v["id"], "PASS" if ok else "FAIL", "" if ok else " " + "; ".join(bad)))
        passed, failed = (passed + 1, failed) if ok else (passed, failed + 1)
    print("\nvectors: %d passed, %d failed" % (passed, failed))
    return 0 if failed == 0 else 1


def cmd_expect(args):
    """核对该任务根的已知节点期望结算（真值来自链上冻结材料，见向量 meta.source）。

    每个期望字段必须**在报告里存在**：字段缺失（ABSENT）与值不同（DIFF）分开报，
    缺失不算通过——报告 schema 静默变窄时应当失败，而不是悄悄跳过。
    --caliber 选期望集：implementation 读 `expect`（默认，保持既有行为），
    ruling 读 `expect_ruling`（裁定口径）。
    """
    rep = json.load(open(args.report, encoding="utf-8"))
    vf = json.load(open(args.nodes, encoding="utf-8"))
    want_root = vf["meta"]["task_root"]
    if want_root != rep.get("task_root"):
        print("task root mismatch: report=%s vector=%s" % (rep.get("task_root"), want_root))
        return 1
    key = "expect" if args.caliber == CALIBER_IMPL else "expect_ruling"
    by = {n["node"]: n for n in rep.get("nodes") or []}
    passed = failed = 0
    for want in vf["nodes"]:
        got, bad = by.get(want["node"]), []
        expect = want.get(key)
        if got is None:
            bad.append("node missing from report")
        elif expect is None:
            bad.append("%s ABSENT in vector for caliber=%s" % (key, args.caliber))
        else:
            for field, wv in expect.items():
                if field not in got:
                    bad.append("%s ABSENT want %r" % (field, wv))
                elif got[field] != wv:
                    bad.append("%s=DIFF %r want %r" % (field, got[field], wv))
        print("  %-6s %s%s" % (want["node"], "PASS" if not bad else "FAIL",
                               "" if not bad else " " + "; ".join(bad)))
        passed, failed = (passed + 1, failed) if not bad else (passed, failed + 1)
    print("\nknown nodes: %d passed, %d failed (vector %s)" % (passed, failed, vf["meta"]["name"]))
    return 0 if failed == 0 else 1


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    r = sub.add_parser("recompute")
    r.add_argument("--snapshot", required=True)
    r.add_argument("--root", required=True)
    r.add_argument("--now", type=int, default=0)
    r.add_argument("--out")
    r.add_argument("--allow-incomplete", action="store_true")
    r.add_argument("--dual-report", action="store_true",
                   help="同时输出实现口径与裁定口径两套计数 + 受影响票清单（canonical JSON 到 stdout）")
    r.add_argument("--no-semantic-check-filter", action="store_true",
                   help="单口径：按裁定口径结算（不因缺 semantic_check 扣票）")
    r.set_defaults(func=cmd_recompute)
    c = sub.add_parser("compare")
    c.add_argument("--a", required=True)
    c.add_argument("--b", required=True)
    c.add_argument("--label-a", default="engine")
    c.add_argument("--label-b", default="mine")
    c.add_argument("--summary")
    c.set_defaults(func=cmd_compare)
    w = sub.add_parser("vectors")
    w.add_argument("--vectors", default=os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                                     "..", "testdata", "metatask-pools-vectors.v1.json"))
    w.set_defaults(func=cmd_vectors)
    e = sub.add_parser("expect")
    e.add_argument("--report", required=True)
    e.add_argument("--nodes", default=os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                                   "..", "testdata", "known-nodes.v1.json"))
    e.add_argument("--caliber", choices=[CALIBER_IMPL, CALIBER_RULING], default=CALIBER_IMPL,
                   help="期望集：implementation 读 expect（默认），ruling 读 expect_ruling")
    e.set_defaults(func=cmd_expect)
    args = ap.parse_args()
    sys.exit(args.func(args))


if __name__ == "__main__":
    main()
