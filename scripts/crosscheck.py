#!/usr/bin/env python3
"""Independently verify the API against the raw CMS files.

This script shares no code with the Go implementation. It downloads the index
and in-network files the server says it ingested, rebuilds the expected
flattened dataset in Python, and then asserts the running API returns identical
row counts, rate statistics, provider filtering, faceting, pagination and sort
order.

Usage:
    python3 scripts/crosscheck.py [--api http://localhost:8080] [--cache .cache]
                                  [--codes 40] [--quick]

Requires the server to be running and ready. Raw files are cached locally, so
only the first run pays the download cost.
"""

from __future__ import annotations

import argparse
import collections
import json
import math
import os
import random
import sys
import urllib.parse
import urllib.request

TIMEOUT = 120


def log(msg: str) -> None:
    print(msg, flush=True)


def fetch_json(url: str):
    with urllib.request.urlopen(url, timeout=TIMEOUT) as r:
        return json.load(r)


def cached_download(url: str, cache_dir: str) -> str:
    """Download url into cache_dir, returning the local path."""
    os.makedirs(cache_dir, exist_ok=True)
    name = urllib.parse.unquote(url.rsplit("/", 1)[-1]) or "file.json"
    safe = "".join(c if c.isalnum() or c in "._-" else "_" for c in name)
    path = os.path.join(cache_dir, safe)
    if os.path.exists(path) and os.path.getsize(path) > 0:
        return path
    log(f"  downloading {name} ...")
    tmp = path + ".partial"
    with urllib.request.urlopen(url, timeout=TIMEOUT) as r, open(tmp, "wb") as out:
        while chunk := r.read(1 << 20):
            out.write(chunk)
    os.replace(tmp, path)
    return path


class Checker:
    def __init__(self) -> None:
        self.failures: list[str] = []
        self.checks = 0

    def eq(self, label, got, want) -> bool:
        self.checks += 1
        if got != want:
            self.failures.append(f"{label}: got {got!r}, want {want!r}")
            log(f"  FAIL {label}: got {got!r}, want {want!r}")
            return False
        return True

    def close(self, label, got, want, rel=1e-9) -> bool:
        """Float comparison. Summation order differs between the two
        implementations, so exact equality is not a meaningful expectation."""
        self.checks += 1
        if not math.isclose(got, want, rel_tol=rel, abs_tol=1e-9):
            self.failures.append(f"{label}: got {got!r}, want {want!r}")
            log(f"  FAIL {label}: got {got!r}, want {want!r}")
            return False
        return True


def percentile(sorted_vals: list[float], p: float) -> float:
    """Linear interpolation between order statistics, matching the server."""
    if not sorted_vals:
        return 0.0
    if len(sorted_vals) == 1:
        return sorted_vals[0]
    pos = p * (len(sorted_vals) - 1)
    lo = int(pos // 1)
    hi = min(lo + 1, len(sorted_vals) - 1)
    frac = pos - lo
    return sorted_vals[lo] * (1 - frac) + sorted_vals[hi] * frac


def build_expected(paths: list[str]):
    """Rebuild the flattened dataset straight from the raw files."""
    rows = []  # (code, code_type, rate, billing_class, group_key)
    npi_to_groups = collections.defaultdict(set)
    ein_to_groups = collections.defaultdict(set)

    for sid, path in enumerate(paths):
        log(f"  parsing {os.path.basename(path)} ...")
        with open(path) as f:
            d = json.load(f)

        ref_groups = collections.defaultdict(list)
        for p in d.get("provider_references", []):
            gid = int(p["provider_group_id"])
            for gi, g in enumerate(p.get("provider_groups", [])):
                tin = g.get("tin") or {}
                ein = ""
                if tin.get("type") == "ein":
                    ein = "".join(c for c in str(tin.get("value", "")) if c.isdigit())
                npis = sorted({str(int(n)) for n in g.get("npi", [])})
                gk = (sid, gid, gi)
                ref_groups[(sid, gid)].append(gk)
                for n in npis:
                    npi_to_groups[n].add(gk)
                if ein:
                    ein_to_groups[ein].add(gk)

        for it in d.get("in_network", []):
            code, ctype = it["billing_code"], it["billing_code_type"]
            for r in it.get("negotiated_rates", []):
                gkeys = []
                for x in r.get("provider_references", []):
                    gkeys.extend(ref_groups.get((sid, int(x)), []))
                for price in r.get("negotiated_prices", []):
                    rate = price["negotiated_rate"]
                    cls = price.get("billing_class")
                    for gk in gkeys:
                        rows.append((code, ctype, rate, cls, gk))

    return rows, npi_to_groups, ein_to_groups


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--api", default=os.environ.get("PTE_API", "http://localhost:8080"))
    ap.add_argument("--cache", default=".cache/crosscheck")
    ap.add_argument("--codes", type=int, default=40, help="random codes to verify")
    ap.add_argument("--providers", type=int, default=25, help="identifiers to verify")
    ap.add_argument("--quick", action="store_true", help="skip the full pagination walk")
    ap.add_argument("--seed", type=int, default=7)
    args = ap.parse_args()

    api_base = args.api.rstrip("/")

    def api(path: str, **params):
        url = f"{api_base}{path}?{urllib.parse.urlencode(params)}"
        return fetch_json(url)

    log(f"API: {api_base}")
    try:
        health = fetch_json(f"{api_base}/api/health")
    except Exception as e:  # noqa: BLE001
        log(f"Cannot reach the API at {api_base}: {e}")
        log("Start the server first (make backend), then re-run.")
        return 2
    if not health.get("ready"):
        log(f"Server is not ready yet (phase: {health.get('phase')}). Wait for ingest to finish.")
        return 2

    meta = api("/api/meta")
    sources = [s for s in meta["sources"] if s["status"] == "ok"]
    log(f"Index: {meta['index_url']}")
    log(f"Ingested sources: {len(sources)}")

    log("Fetching raw source files (cached after the first run)...")
    paths = [cached_download(s["url"], args.cache) for s in sources]

    log("Rebuilding the expected dataset in Python...")
    rows, npi_to_groups, ein_to_groups = build_expected(paths)
    log(f"  expected rows: {len(rows):,}")

    by_code: dict[str, list] = collections.defaultdict(list)
    for r in rows:
        by_code[r[0]].append(r)

    c = Checker()
    random.seed(args.seed)

    # 1. Dataset totals ------------------------------------------------------
    log("\n[1/8] dataset totals")
    c.eq("total rows", meta["counts"]["rows"], len(rows))
    c.eq("distinct codes", meta["counts"]["distinct_codes"], len(by_code))

    # 2. Per-code totals and rate statistics ---------------------------------
    sample = random.sample(sorted(by_code), min(args.codes, len(by_code)))
    busiest = [k for k, _ in collections.Counter({k: len(v) for k, v in by_code.items()}).most_common(5)]
    codes = list(dict.fromkeys(sample + busiest))
    log(f"[2/8] {len(codes)} codes: totals, min/max/mean/median/p25/p75, histogram")
    for code in codes:
        exp = by_code[code]
        res = api("/api/rates", code=code, page_size=1)
        if not c.eq(f"code {code} total", res["pagination"]["total"], len(exp)):
            continue
        rates = sorted(r[2] for r in exp)
        st = res["stats"]
        c.close(f"code {code} min", st["min"], rates[0])
        c.close(f"code {code} max", st["max"], rates[-1])
        c.close(f"code {code} mean", st["mean"], sum(rates) / len(rates))
        c.close(f"code {code} median", st["median"], percentile(rates, 0.50))
        c.close(f"code {code} p25", st["p25"], percentile(rates, 0.25))
        c.close(f"code {code} p75", st["p75"], percentile(rates, 0.75))
        c.eq(f"code {code} histogram total", sum(b["count"] for b in st["histogram"]), len(exp))

    # 3. NPI filtering -------------------------------------------------------
    log(f"[3/8] {args.providers} NPI filters")
    npis = [n for n in npi_to_groups if n != "0"]
    for npi in random.sample(npis, min(args.providers, len(npis))):
        gset = npi_to_groups[npi]
        codes_for = collections.Counter(r[0] for r in rows if r[4] in gset)
        if not codes_for:
            continue
        code, want = codes_for.most_common(1)[0]
        res = api("/api/rates", code=code, npi=npi, page_size=5)
        c.eq(f"npi {npi} + code {code}", res["pagination"]["total"], want)
        for r in res["results"]:
            c.eq(f"npi {npi} matched_npi surfaced", r["provider_group"].get("matched_npi"), npi)

    # 4. EIN filtering, bare and dashed --------------------------------------
    log(f"[4/8] {args.providers} EIN filters (bare and dashed spellings)")
    for ein in random.sample(sorted(ein_to_groups), min(args.providers, len(ein_to_groups))):
        gset = ein_to_groups[ein]
        codes_for = collections.Counter(r[0] for r in rows if r[4] in gset)
        if not codes_for:
            continue
        code, want = codes_for.most_common(1)[0]
        c.eq(f"ein {ein} + code {code}", api("/api/rates", code=code, ein=ein, page_size=1)["pagination"]["total"], want)
        dashed = f"{ein[:2]}-{ein[2:]}"
        c.eq(f"ein {dashed} + code {code}", api("/api/rates", code=code, ein=dashed, page_size=1)["pagination"]["total"], want)

    # 5. NPI and EIN intersect, never union ----------------------------------
    log("[5/8] NPI + EIN intersection")
    ein_of_group = {}
    for e, gs in ein_to_groups.items():
        for g in gs:
            ein_of_group[g] = e
    done = 0
    for npi in random.sample(npis, min(500, len(npis))):
        gk = next(iter(npi_to_groups[npi]))
        ein = ein_of_group.get(gk)
        if not ein:
            continue
        both = npi_to_groups[npi] & ein_to_groups[ein]
        codes_for = collections.Counter(r[0] for r in rows if r[4] in both)
        if not codes_for:
            continue
        code, want = codes_for.most_common(1)[0]
        c.eq(f"npi {npi} + ein {ein} + code {code}",
             api("/api/rates", code=code, npi=npi, ein=ein, page_size=1)["pagination"]["total"], want)
        done += 1
        if done >= 10:
            break

    # 6. billing_class partitions the result set -----------------------------
    log("[6/8] billing_class partitions")
    for code in random.sample(sorted(by_code), min(10, len(by_code))):
        total = api("/api/rates", code=code, page_size=1)["pagination"]["total"]
        parts = sum(
            api("/api/rates", code=code, billing_class=cls, page_size=1)["pagination"]["total"]
            for cls in ("professional", "institutional")
        )
        c.eq(f"code {code} class parts sum to total", parts, total)

    # 7. Pagination covers the result set exactly ----------------------------
    busiest_code = busiest[0]
    total = len(by_code[busiest_code])
    if args.quick:
        log(f"[7/8] pagination walk skipped (--quick); busiest code {busiest_code} has {total:,} rows")
    else:
        log(f"[7/8] paging through every row of code {busiest_code} ({total:,} rows)")
        seen, page, rate_sum = 0, 1, 0.0
        while True:
            res = api("/api/rates", code=busiest_code, page=page, page_size=500, sort="rate_asc")
            if not res["results"]:
                break
            seen += len(res["results"])
            rate_sum += sum(r["negotiated_rate"] for r in res["results"])
            page += 1
            if page > 2000:
                break
        c.eq(f"code {busiest_code} pagination row count", seen, total)
        c.close(f"code {busiest_code} pagination rate sum", rate_sum,
                sum(sorted(r[2] for r in by_code[busiest_code])), rel=1e-9)

    # 8. Sort order ----------------------------------------------------------
    log("[8/8] sort order")
    asc = [r["negotiated_rate"] for r in api("/api/rates", code=busiest_code, sort="rate_asc", page_size=200)["results"]]
    desc = [r["negotiated_rate"] for r in api("/api/rates", code=busiest_code, sort="rate_desc", page_size=200)["results"]]
    c.eq("rate_asc is non-decreasing", asc == sorted(asc), True)
    c.eq("rate_desc is non-increasing", desc == sorted(desc, reverse=True), True)

    log("\n" + "=" * 72)
    if c.failures:
        log(f"{len(c.failures)} FAILURES out of {c.checks} checks")
        for f in c.failures[:40]:
            log(f"  - {f}")
        return 1
    log(f"ALL {c.checks} CROSS-CHECKS PASSED")
    return 0


if __name__ == "__main__":
    sys.exit(main())
