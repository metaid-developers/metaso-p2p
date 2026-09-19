#!/usr/bin/env python3
"""
metatask_replay.py — metaTask v0 重放引擎（状态机计算，零第三方依赖）

输入: --events <dir>  目录内存放 omni_read pins_by_path 的原始输出（.json，每文件一个响应，
      可多页多文件，脚本自动合并去重），文件名任意但建议 <path名>.json（task/claim/...）
输出: 节点状态表 + 信誉榜（stdout JSON）

采集数据（由执行 bot 用 omni_read 完成，务必 cursor 翻页到空页）:
  对 p in [task,tree,spec,claim,release,submission,verify]:
    omni_read pins_by_path path=/protocols/metatask/<p> size=5，逐页 cursor 直到空页，
    每页响应原样存为 events/<p>.<n>.json

重放规则（/protocols/metatask v1.0.0 + 修订清单 #8/#9 票务过滤，v1.2.0）:
  claim      同节点最早有效 claim 持有（块高+tx序）；release 撤销
  submission 同 claim 周期只认最早一条，且 claimid 必须等于当期持有 claim
  verify     复核者≠提交者≠任务根作者；每 bot 每 target 一票（多票取最后一票；
             无效票先过滤——#9 缺 semantic_check 计票无效，#8 fail 缺 failreason
             按 invalid 不占 fail 票；票级「视为未投票」，畸形票不构成撤回）；
             pass>=quorum 且有效 fail=0 → verified；
             任一有效 fail → 打回、节点立回 open（裁定三：不等后续票）
  存量事件   统一按 v1.2.0 语义重放，不按事件版本分叉（定案③）
"""
import json, sys, os, glob, collections

def load_events(d):
    pins = {}
    for f in sorted(glob.glob(os.path.join(d, "*.json"))):
        try:
            doc = json.load(open(f, encoding="utf-8"))
        except Exception:
            continue
        # 兼容三种形态: omni_read 响应 / {"data":{list}} / 裸 list / 单 pin 对象
        cands = []
        if isinstance(doc, dict):
            data = doc.get("data") or doc
            lst = data.get("list") if isinstance(data, dict) else None
            if lst is None and isinstance(data, dict) and "id" in data: lst = [data]
            if lst is None and "id" in doc: lst = [doc]
            cands = lst or []
        elif isinstance(doc, list):
            cands = doc
        for p in cands:
            if isinstance(p, dict) and p.get("id"):
                pins[p["id"]] = p  # 去重（同 pin 多页/多文件）
    return list(pins.values())

def cbody(pin):
    """解出事件 payload：优先 contentBody(base64)，退回 contentSummary"""
    import base64
    cb = pin.get("contentBody")
    if cb:
        try:
            return json.loads(base64.b64decode(cb).decode("utf-8"))
        except Exception:
            pass
    cs = pin.get("contentSummary")
    if cs:
        try:
            return json.loads(cs)
        except Exception:
            pass
    return {}

def okey(p):
    gh = p.get("genesisHeight", -1)
    gh = gh if isinstance(gh, int) and gh >= 0 else 10**12
    tx = p.get("txIndex", 0) if isinstance(p.get("txIndex"), int) else 0
    return (gh, tx, p.get("seenTime") or p.get("timestamp") or 0, p.get("id", ""))


# ---- §3.7 verify 票有效性门（修订清单 #8/#9，chair 定案 2026-09-15，v1.2.0）----
# 无效票在 last-per-bot 之前过滤：#9 原文「该票……仅视为未投票」是票级无效，
# 不是 bot 级撤回——畸形票不冲掉同 bot 在案的有效票（否则可用畸形票悄悄撤回
# 在案 fail、绕过打回）。存量事件统一按本语义重放（定案③）。
def vote_invalid(b):
    """返回无效原因串；有效票返回 None。
    #9: semantic_check 缺失/空 → 计票无效（不计 pass/fail、不触发打回，同 method 为空先例）
    #8: verdict=fail 且 failreason 缺失/空 → invalid（不占 fail 票、不触发打回）；
        evidence 自 v1.2 起降为可选加分，不再是 §4.4 要件"""
    if not str(b.get("semantic_check") or "").strip():
        return "missing_semantic_check"   # #9（含 pass/fail 两侧——计票无效）
    if b.get("verdict") == "fail" and not str(b.get("failreason") or "").strip():
        return "missing_failreason"       # #8（fail 通道——按 invalid，不打回）
    return None


# ---- §4.7 claimPrecheck (revision item 7, chair-approved 2026-09-15) ----
# guard_state(): complete node-state derivation for entry-side precheck.
# Extends the replay rules with §4.2 claimTTL and §4.3 reviewWindow, which the
# minimal replay above does not expire (it reports final-state only).
def guard_state(allpins, root, node, now=None, claim_ttl_hours=None, verify_window_hours=None):
    import time as _time
    if now is None: now = int(_time.time())
    ev = collections.defaultdict(list)
    for p in allpins:
        path = (p.get("path") or "").rsplit("/", 1)[-1]
        if path in ("task", "tree", "spec", "claim", "release", "submission", "verify"):
            ev[path].append(p)
    for k in ev: ev[k].sort(key=okey)
    root_pin = next((p for p in ev["task"] if p["id"] == root), None)
    if not root_pin: return {"verdict": "error", "reason": "task-root-not-in-events"}
    root_author = root_pin.get("globalMetaId")
    policy = cbody(root_pin).get("policy", {})
    ttl_h = claim_ttl_hours if claim_ttl_hours is not None else int(policy.get("claim_ttl_hours", 0) or 0)
    win_h = verify_window_hours if verify_window_hours is not None else int(policy.get("verify_window_hours", 0) or 0)
    quorum = int(policy.get("verify_quorum", 2))

    holders = {}
    claims_all = [p for p in ev["claim"] if cbody(p).get("taskid") == root and str(cbody(p).get("node")) == str(node)]
    timeline = sorted(claims_all + [p for p in ev["release"] if cbody(p).get("taskid") == root and str(cbody(p).get("node")) == str(node)], key=okey)
    inflight = False
    for p in timeline:
        b = cbody(p); act = p.get("path", "").rsplit("/", 1)[-1]
        key = (root, str(node))
        if act == "claim":
            if key not in holders:
                holders[key] = {"pin": p["id"], "claimant": p.get("globalMetaId"), "t0": p.get("timestamp") or p.get("seenTime") or 0}
                if p.get("genesisHeight", -1) < 0: inflight = True   # unconfirmed claim in flight (§4.7 conservative)
        else:
            if holders.get(key, {}).get("pin") == b.get("claimid"):
                holders.pop(key, None)   # §3.5: node returns open; ignored claims stay ignored (no revival)
    h = holders.get((root, str(node)))

    # effective submission (§4.6 anchored to current holder)
    sub = None
    if h:
        for p in ev["submission"]:
            b = cbody(p)
            if b.get("taskid") != root or str(b.get("node")) != str(node): continue
            if b.get("claimid") == h["pin"]: sub = {"pin": p["id"], "author": p.get("globalMetaId"), "t": p.get("timestamp") or p.get("seenTime") or 0}; break

    # verify votes (§4.4 last-per-bot；v1.2.0 #8/#9: 无效票先过滤，再取最后有效票)
    last = {}
    if sub:
        for p in ev["verify"]:
            b = cbody(p)
            if b.get("targetid") != sub["pin"]: continue
            if vote_invalid(b): continue   # 缺 semantic_check（#9）/ fail 缺 failreason（#8）→ 视为未投票
            bot = p.get("globalMetaId")
            if bot: last[bot] = b
    good = sum(1 for bot, b in last.items() if b.get("verdict") == "pass" and bot not in (sub["author"], root_author)) if sub else 0
    bad = sum(1 for bot, b in last.items() if b.get("verdict") == "fail") if sub else 0   # 过滤后 fail 必带 failreason（§4.4 v1.2）

    if inflight and h:
        state = "claimed"   # conservative: unconfirmed claim in flight blocks new claims (t47 incident mirror)
    elif sub and good >= quorum and not bad:
        state = "verified"
    elif sub and bad:
        state = "open"   # §4.4 v1.2（#8）: 任一有效 fail（带 failreason）→ 打回，节点立回 open（裁定三：不等后续票）
    elif h and not sub and ttl_h and (now - int(h["t0"])) > ttl_h * 3600:
        state = "open"   # §4.2 claimTTL: claim expired without submission
    elif sub and win_h and (now - int(sub["t"])) > win_h * 3600 and good < quorum:
        state = "open"   # §4.3 reviewWindow expired without quorum
    elif h:
        state = "claimed"
    else:
        state = "open"
    return {"verdict": "allow" if state == "open" else "reject",
            "state": state,
            "reason": None if state == "open" else f"claim-rejected:{node}:{state}",
            "detail": {"holder": (h or {}).get("claimant"), "submission": (sub or {}).get("pin"), "pass": good, "fail_with_failreason": bad}}

def main():
    ap = sys.argv[1:]
    if not ap or ap[0] in ("-h", "--help"):
        print(__doc__); sys.exit(0)
    if "--guard" in ap:
        gi = ap.index("--guard")
        gtask, gnode = ap[gi + 1], ap[gi + 2]
        evdir = ap[ap.index("--events") + 1] if "--events" in ap else "events"
        gnow = None
        if "--now" in ap: gnow = int(ap[ap.index("--now") + 1])
        pins = load_events(evdir)
        out = guard_state(pins, gtask, gnode, now=gnow)
        print(json.dumps(out, ensure_ascii=False))
        sys.exit(0 if out["verdict"] == "allow" else 2)
    evdir = ap[ap.index("--events") + 1] if "--events" in ap else ap[0]
    root = None
    if "--root" in ap: root = ap[ap.index("--root") + 1]

    allpins = load_events(evdir)
    ev = collections.defaultdict(list)
    for p in allpins:
        path = (p.get("path") or "").rsplit("/", 1)[-1]
        if path in ("task", "tree", "spec", "claim", "release", "submission", "verify"):
            ev[path].append(p)
    for k in ev: ev[k].sort(key=okey)

    task_pins = [p for p in ev["task"]]
    if not task_pins:
        print(json.dumps({"error": "no task pins in events"})); sys.exit(2)
    root_pin = None
    if root:
        root_pin = next((p for p in task_pins if p["id"] == root), None)
        if not root_pin: print(json.dumps({"error": f"root {root} not found"})); sys.exit(2)
    else:
        root_pin = task_pins[-1]; root = root_pin["id"]
    root_author = root_pin.get("globalMetaId")
    task_body = cbody(root_pin)
    policy = task_body.get("policy", {})
    quorum = int(policy.get("verify_quorum", 2))

    # claims: 同 (taskid,node) 最早有效；release 撤销
    holders, released_claims = {}, set()
    # §4.1: process claim/release in global (height,tx) order; a released claim
    # is no longer 有效, so the earliest non-released claim holds the lock.
    claims_by_lock = collections.defaultdict(list)
    for p in ev["claim"]:
        b = cbody(p)
        if b.get("taskid") != root: continue
        claims_by_lock[(root, str(b.get("node")))].append(p)
    released_ids = set()
    rels = []
    for p in ev["release"]:
        b = cbody(p)
        if b.get("taskid") != root: continue
        rels.append((okey(p), b.get("claimid"), (root, str(b.get("node")))))
    for key, cl in claims_by_lock.items():
        cl.sort(key=okey)
        for p in cl: released_ids.add(p["id"])
    # walk global order: claims insert (if lock free), releases kill
    timeline = sorted([p for p in ev["claim"] if cbody(p).get("taskid") == root] +
                      [p for p in ev["release"] if cbody(p).get("taskid") == root], key=okey)
    for p in timeline:
        b = cbody(p); act = p.get("path", "").rsplit("/", 1)[-1]
        key = (root, str(b.get("node")))
        if act == "claim":
            if key not in holders: holders[key] = {"pin": p["id"], "claimant": p.get("globalMetaId")}
            # else: ignored permanently (§4.1); no revival after release (§3.5 节点立回 open)
        else:
            if holders.get(key, {}).get("pin") == b.get("claimid"):
                holders.pop(key, None)

    # submissions: 同 claim 周期最早一条，且 claimid==当期持有 claim
    subs = {}
    for p in ev["submission"]:
        b = cbody(p)
        if b.get("taskid") != root: continue
        node = str(b.get("node")); cid = b.get("claimid")
        h = holders.get((root, node))
        if not h or h["pin"] != cid: continue
        key = (node, cid)
        if key not in subs:
            subs[key] = {"pin": p["id"], "author": p.get("globalMetaId"), "body": b}

    # verifies: 每 bot 每 target 最后一票（v1.2.0 #8/#9: 无效票先过滤，再取最后有效票）
    last = {}
    for p in ev["verify"]:
        b = cbody(p)
        tgt, bot = b.get("targetid"), p.get("globalMetaId")
        if tgt and bot and not vote_invalid(b): last[(tgt, bot)] = b

    def verified_sub(node):
        for (n, cid), s in subs.items():
            if n != node: continue
            tgt, author = s["pin"], s["author"]
            votes = [(bot, b) for (t, bot), b in last.items() if t == tgt]
            good = [b for bot, b in votes if b.get("verdict") == "pass" and bot not in (author, root_author)]
            bad = [b for bot, b in votes if b.get("verdict") == "fail"]   # 过滤后 fail 必带 failreason（§4.4 v1.2）
            if len(good) >= quorum and not bad:
                return s, good
        return None, []

    nodes = set()
    tree_pin = task_body.get("treeid")
    if tree_pin:
        tb = next((cbody(p) for p in ev["tree"] if p["id"] == tree_pin), None)
        if tb: nodes = {str(n["id"]) for n in tb.get("nodes", [])}
    nodes |= {k[1] for k in holders} | {k[0] for k in subs}

    rep = collections.Counter()
    nodes_out = {}
    for node in sorted(nodes, key=lambda x: (len(x), x)):
        s, votes = verified_sub(node)
        h = holders.get((root, node))
        sub_pin = s["pin"] if s else None
        if not sub_pin and h:
            ss = subs.get((node, h["pin"]))
            if ss: sub_pin = ss["pin"]
        if s: rep[s["author"]] += 1
        nodes_out[node] = {"status": "verified" if s else ("claimed" if h else "open"),
                           "holder": (h or {}).get("claimant"), "submission": sub_pin,
                           "effective_pass": len(votes)}
    print(json.dumps({"task_root": root, "quorum": quorum,
                      "all_verified": all(n.get("status") == "verified" for n in nodes_out.values()) and len(nodes_out) > 0,
                      "nodes": nodes_out,
                      "reputation": dict(rep), "verify_votes": len(last)}, ensure_ascii=False, indent=2))

if __name__ == "__main__":
    main()
