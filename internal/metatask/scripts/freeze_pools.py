#!/usr/bin/env python3
"""freeze_pools.py — 抓取 metaTask 七池冻结快照（宪章 §〇.1 的材料获取步）。

产物（--out 目录下）
  pages/<pool>/<n>.json       每次 list 请求的**原始响应字节**（未改）
  pages/<pool>/<n>.headers    同次请求的响应头（Date 作为抓取时刻凭证）
  content/<pinId>.bin         每个 pin 从 content 端点取回的**原始载荷字节**
  manifest.json               冻结四件套：路径 / cutoff 时刻 / 每页水位与逐页哈希 / 每 pin 字节长度与 sha256

用法
  python3 freeze_pools.py --out <dir> [--size 50] [--base https://manapi.metaid.io] [--events <dir>]

判据（§〇 拒收规则）
  - 只有 list 显式返回空数组/空件**且** nextCursor 为空才算页尽；字段缺失一律标 UNKNOWN。
  - 满页却无 cursor → FULL_PAGE_NO_CURSOR（UNKNOWN，不是页尽）。
  - content 字节数 != list 声明的 contentLength → 标 TRUNCATED（拒收，不静默采信）。
  - 逐 pin 取 content 时必须用**裸 pinId**，带扩展名会被服务端拒绝。

--events <dir> 额外产出 metatask-replay 引擎可直接吃的事件目录：每个 path 一个 JSON 数组，
pin 对象带 contentBody(base64) = 原始载荷字节（**不用 contentSummary**，后者对长载荷会静默截断）。
零第三方依赖（标准库 + 系统 curl）。
"""
import argparse
import base64
import hashlib
import json
import os
import subprocess
import sys
import time
from urllib.parse import urlencode

PATHS = ["task", "tree", "spec", "claim", "release", "submission", "verify"]


def curl(url, out_path, header_path):
    r = subprocess.run(
        ["curl", "-sS", "-m", "60", "-D", header_path, "-o", out_path, "-w", "%{http_code}", url],
        capture_output=True, text=True)
    if r.returncode != 0:
        raise RuntimeError("curl failed rc=%d %s" % (r.returncode, r.stderr.strip()))
    return r.stdout.strip()


def sha256_file(p):
    h = hashlib.sha256()
    with open(p, "rb") as f:
        for chunk in iter(lambda: f.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()


def header_date(hp):
    try:
        for line in open(hp, encoding="utf-8", errors="replace"):
            if line.lower().startswith("date:"):
                return line.split(":", 1)[1].strip()
    except OSError:
        pass
    return None


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--out", required=True)
    ap.add_argument("--base", default="https://manapi.metaid.io")
    ap.add_argument("--size", type=int, default=50)
    ap.add_argument("--events", default="")
    ap.add_argument("--max-pages", type=int, default=400)
    args = ap.parse_args()

    out = args.out
    os.makedirs(out, exist_ok=True)
    manifest = {"base": args.base, "size": args.size, "frozen_at_unix": int(time.time()),
                "frozen_at": time.strftime("%Y-%m-%dT%H:%M:%S%z"), "paths": {}, "pins": {}}
    seen = {}

    for path in PATHS:
        pdir = os.path.join(out, "pages", path)
        os.makedirs(pdir, exist_ok=True)
        proto = "/protocols/metatask/" + path
        cursor, pages, term = "", [], "OK"
        for n in range(args.max_pages):
            q = {"path": proto, "size": str(args.size)}
            if cursor:
                q["cursor"] = cursor
            url = args.base + "/api/pin/path/list?" + urlencode(q)
            jf, hf = os.path.join(pdir, "%d.json" % n), os.path.join(pdir, "%d.headers" % n)
            code = curl(url, jf, hf)
            raw = open(jf, "rb").read()
            try:
                doc = json.loads(raw.decode("utf-8"))
            except Exception as e:
                term = "LIST_PARSE_ERROR"
                pages.append({"n": n, "http": code, "error": str(e), "sha256": hashlib.sha256(raw).hexdigest()})
                break
            data = doc.get("data")
            if not isinstance(data, dict) or "list" not in data:
                term = "LIST_FIELD_MISSING"
                pages.append({"n": n, "http": code, "keys": sorted(doc.keys()),
                              "sha256": hashlib.sha256(raw).hexdigest()})
                break
            items, nxt = data.get("list"), data.get("nextCursor") or ""
            pages.append({"n": n, "http": code, "count": None if items is None else len(items),
                          "next_cursor_present": bool(nxt), "next_cursor": nxt,
                          "total": data.get("total"), "sha256": hashlib.sha256(raw).hexdigest(),
                          "date": header_date(hf)})
            for p in items or []:
                pid = p.get("id")
                if pid and pid not in seen:
                    seen[pid] = {"path": p.get("path"), "contentLength": p.get("contentLength"),
                                 "timestamp": p.get("timestamp"), "seenTime": p.get("seenTime"),
                                 "genesisHeight": p.get("genesisHeight"), "txIndex": p.get("txIndex"),
                                 "globalMetaId": p.get("globalMetaId"), "number": p.get("number"),
                                 "genesisFee": p.get("genesisFee"), "outputValue": p.get("outputValue")}
            if items is None and not nxt:
                term = "LIST_NULL_EMPTY_CURSOR"
                break
            if items is None:
                term = "LIST_NULL_WITH_CURSOR"
                break
            if len(items) == 0 and not nxt:
                term = "EMPTY_PAGE_CURSOR_EXHAUSTED"
                break
            if len(items) == 0:
                term = "EMPTY_PAGE_WITH_CURSOR"
                break
            if not nxt:
                term = "FULL_PAGE_NO_CURSOR"
                break
            cursor = nxt
        manifest["paths"][path] = {"protocol_path": proto, "pages": pages, "termination": term,
                                   "pages_fetched": len(pages),
                                   "unique_pins": sum(1 for p in seen.values() if p["path"] == proto)}
        print("[%s] pages=%d pins=%d term=%s" % (path, len(pages),
              manifest["paths"][path]["unique_pins"], term), flush=True)

    cdir = os.path.join(out, "content")
    os.makedirs(cdir, exist_ok=True)
    for i, pid in enumerate(sorted(seen), 1):
        cp, hf = os.path.join(cdir, pid + ".bin"), os.path.join(cdir, pid + ".headers")
        try:
            code = curl(args.base + "/content/" + pid, cp, hf)
        except Exception as e:
            seen[pid].update({"content_http": None, "content_error": str(e)})
            continue
        size = os.path.getsize(cp) if os.path.exists(cp) else 0
        cl = seen[pid].get("contentLength")
        rec = {"content_http": code, "content_bytes": size,
               "content_sha256": sha256_file(cp) if size else None}
        if code != "200" or size == 0:
            rec["content_state"] = "UNREADABLE"
        elif isinstance(cl, int) and cl > 0 and size != cl:
            rec["content_state"] = "TRUNCATED"
        else:
            rec["content_state"] = "OK"
        seen[pid].update(rec)
        if i % 50 == 0:
            print("  content %d/%d" % (i, len(seen)), flush=True)

    manifest["pins"] = seen
    states = {}
    for p in seen.values():
        states[p.get("content_state")] = states.get(p.get("content_state"), 0) + 1
    manifest["content_state_counts"] = states
    with open(os.path.join(out, "manifest.json"), "w", encoding="utf-8") as f:
        json.dump(manifest, f, ensure_ascii=False, indent=2, sort_keys=True)
    print("pins=%d content_states=%s" % (len(seen), states))
    print("manifest sha256=%s" % sha256_file(os.path.join(out, "manifest.json")))

    if args.events:
        os.makedirs(args.events, exist_ok=True)
        for path in PATHS:
            pins = {}
            for n in range(args.max_pages):
                f = os.path.join(out, "pages", path, "%d.json" % n)
                if not os.path.exists(f):
                    break
                d = json.load(open(f, encoding="utf-8"))
                for p in (d.get("data") or {}).get("list") or []:
                    pins[p["id"]] = p
            enriched, missing = [], 0
            for pid, p in pins.items():
                cp = os.path.join(cdir, pid + ".bin")
                raw = open(cp, "rb").read() if os.path.exists(cp) else b""
                if not raw:
                    missing += 1
                    continue
                q = dict(p)
                q["contentBody"] = base64.b64encode(raw).decode()
                enriched.append(q)
            with open(os.path.join(args.events, path + ".json"), "w", encoding="utf-8") as f:
                json.dump(enriched, f, ensure_ascii=False)
            print("events/%s.json: %d pins (missing payload %d)" % (path, len(enriched), missing))


if __name__ == "__main__":
    main()
