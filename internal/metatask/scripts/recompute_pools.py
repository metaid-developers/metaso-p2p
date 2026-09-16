#!/usr/bin/env python3
"""recompute_pools.py — metaTask 三池聚合的可复算工具（零第三方依赖）。

子命令
  recompute  从冻结快照（pages/ + content/）独立复算：游标翻页到空页、§〇 载荷
             验收、v1.2.0 结算、三池读数、费用与时间戳元数据，输出 JSON。
  compare    逐项对照两份聚合结果（引擎 / 本实现），输出对照表 + 差异清单；
             0 差异退出码 0，有差异退出码 1。
  vectors    执行 testdata/metatask-pools-vectors.v1.json 的自测向量（含 N1 截断副本）。

用法
  python3 recompute_pools.py recompute --snapshot <dir> --root <pinId> --now <unix> --out <file>
  python3 recompute_pools.py compare --a <engine.json> --b <mine.json> [--label-a X --label-b Y]
  python3 recompute_pools.py vectors --vectors <file>

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


def sha256_bytes(b):
    return hashlib.sha256(b).hexdigest()


def sha256_file(p):
    with open(p, "rb") as f:
        return hashlib.sha256(f.read()).hexdigest()


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


def vote_invalid(b):
    if not present(b.get("semantic_check")):
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

def settle(pins_by_path, root, now):
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
        if tgt and bot and not vote_invalid(b):
            last[(tgt, bot)] = b

    def votes_for(target):
        valid, invalid, pr, fr = [], [], 0, 0
        for p in verifies:
            b = p["_payload"]
            if b.get("targetid") != target:
                continue
            pr += 1 if b.get("verdict") == "pass" else 0
            fr += 1 if b.get("verdict") == "fail" else 0
            reason = vote_invalid(b)
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
                if b.get("targetid") != sub["pin"] or vote_invalid(b):
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
            if b.get("targetid") != s["pin"] or vote_invalid(b):
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
                        "invalid_votes": sum(1 for p in in_scope_verifies if vote_invalid(p["_payload"])),
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
    rep = settle(pins_by_path, args.root, args.now)
    rep["enumeration"] = walk
    blob = json.dumps(rep, ensure_ascii=False, indent=2, sort_keys=True)
    if args.out:
        open(args.out, "w").write(blob + "\n")
        print("wrote %s" % args.out)
    else:
        print(blob)
    if not walk["complete"] and not args.allow_incomplete:
        print("INCOMPLETE ENUMERATION: %s" % walk["unresolved"], file=sys.stderr)
        return 2
    return 0


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
        pins_by_path = {p: [] for p in PATHS}
        root = ""
        for e in v["events"]:
            raw = json.dumps(e["payload"]).encode()
            state, obj, _ = accept_payload(raw, len(raw), "vector")
            pins_by_path[e["path"].rsplit("/", 1)[-1]].append(dict(e, _payload=obj, _state=state, _reason="",
                                                              _bytes=raw))
            if e["path"].endswith("/task") and not root:
                root = e["id"]
        rep = settle(pins_by_path, root, 20000)
        by = {n["node"]: n for n in rep["nodes"]}
        bad = []
        if rep["quorum"] != v["expect"]["quorum"]:
            bad.append("quorum=%s" % rep["quorum"])
        for node, want in v["expect"]["nodes"].items():
            got = by.get(node)
            if got is None:
                bad.append("%s missing" % node)
                continue
            for k, wv in want.items():
                if got.get(k) != wv:
                    bad.append("%s.%s=%s want %s" % (node, k, got.get(k), wv))
        ok = not bad
        print("  %-44s %s%s" % (v["id"], "PASS" if ok else "FAIL", "" if ok else " " + "; ".join(bad)))
        passed, failed = (passed + 1, failed) if ok else (passed, failed + 1)
    print("\nvectors: %d passed, %d failed" % (passed, failed))
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
    args = ap.parse_args()
    sys.exit(args.func(args))


if __name__ == "__main__":
    main()
