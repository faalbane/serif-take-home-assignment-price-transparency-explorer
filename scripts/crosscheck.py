"""Independent verification of the Go API against the raw CMS files.

Builds the expected flattened dataset in Python directly from the source JSON,
then asserts the API returns identical totals, rate statistics and provider
filtering results. Nothing is shared with the Go implementation.
"""
import json, urllib.request, urllib.parse, collections, statistics, random, sys

API = "http://localhost:8099"
FILES = ['in-network-ex.json', 'in-network-es.json']

# ---- build expected dataset independently ------------------------------------
rows = []            # (code, code_type, rate, billing_class, gid_key)
npi_to_groups = collections.defaultdict(set)
ein_to_groups = collections.defaultdict(set)

for sid, fn in enumerate(FILES):
    d = json.load(open(fn))
    groups = {}      # group key -> (ein_digits, npis)
    ref_groups = collections.defaultdict(list)
    for p in d['provider_references']:
        gid = int(p['provider_group_id'])
        for gi, g in enumerate(p['provider_groups']):
            tin = g.get('tin', {})
            ein = ''.join(ch for ch in str(tin.get('value', '')) if ch.isdigit()) if tin.get('type') == 'ein' else ''
            npis = sorted({str(int(n)) for n in g.get('npi', [])})
            key = (sid, gid, gi)
            groups[key] = (ein, npis)
            ref_groups[(sid, gid)].append(key)
            for n in npis:
                npi_to_groups[n].add(key)
            if ein:
                ein_to_groups[ein].add(key)

    for it in d['in_network']:
        code, ctype = it['billing_code'], it['billing_code_type']
        for r in it.get('negotiated_rates', []):
            gkeys = []
            for x in r.get('provider_references', []):
                gkeys.extend(ref_groups.get((sid, int(x)), []))
            for p in r['negotiated_prices']:
                for gk in gkeys:
                    rows.append((code, ctype, p['negotiated_rate'], p.get('billing_class'), gk))

print(f"expected total rows: {len(rows)}")

by_code = collections.defaultdict(list)
for r in rows:
    by_code[r[0]].append(r)

def pct(sorted_vals, p):
    if len(sorted_vals) == 1:
        return sorted_vals[0]
    pos = p * (len(sorted_vals) - 1)
    lo, hi = int(pos // 1), min(int(pos // 1) + 1, len(sorted_vals) - 1)
    frac = pos - lo
    return sorted_vals[lo] * (1 - frac) + sorted_vals[hi] * frac

def api(path, **params):
    url = API + path + "?" + urllib.parse.urlencode(params)
    with urllib.request.urlopen(url, timeout=60) as r:
        return json.load(r)

failures = []
def close(a, b, tol=1e-9):
    """Floating-point comparison. Summation order differs between the two
    implementations, so exact equality is not a meaningful expectation."""
    import math
    return math.isclose(a, b, rel_tol=tol, abs_tol=1e-9)

def checkf(label, got, want):
    if not close(got, want):
        failures.append(f"{label}: got {got!r}, want {want!r}")
        print(f"  FAIL {label}: got {got!r}, want {want!r}")

def check(label, got, want):
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")
        print(f"  FAIL {label}: got {got!r}, want {want!r}")
    return got == want

# ---- 1. global row count -----------------------------------------------------
meta = api("/api/meta")
check("total rows", meta['counts']['rows'], len(rows))
check("distinct codes", meta['counts']['distinct_codes'], len(by_code))

# ---- 2. per-code totals and stats, over a random sample ----------------------
random.seed(7)
codes = random.sample(sorted(by_code), 40)
# plus the highest-volume codes, where an off-by-one is most likely to hide
codes += [c for c, _ in collections.Counter({c: len(v) for c, v in by_code.items()}).most_common(5)]

print(f"\nchecking {len(codes)} codes (totals + min/max/mean/median/p25/p75)...")
for code in codes:
    exp = by_code[code]
    res = api("/api/rates", code=code, page_size=1)
    if not check(f"code {code} total", res['pagination']['total'], len(exp)):
        continue
    rates = sorted(r[2] for r in exp)
    st = res['stats']
    checkf(f"code {code} min", st['min'], rates[0])
    checkf(f"code {code} max", st['max'], rates[-1])
    checkf(f"code {code} mean", st['mean'], sum(rates) / len(rates))
    checkf(f"code {code} median", st['median'], pct(rates, .50))
    checkf(f"code {code} p25", st['p25'], pct(rates, .25))
    checkf(f"code {code} p75", st['p75'], pct(rates, .75))
    hist_total = sum(b['count'] for b in st['histogram'])
    check(f"code {code} histogram sums to count", hist_total, len(exp))

# ---- 3. NPI filtering --------------------------------------------------------
print("\nchecking NPI filters...")
npi_candidates = [n for n in npi_to_groups if n != '0']
for npi in random.sample(npi_candidates, 25):
    gset = npi_to_groups[npi]
    codes_for_npi = collections.Counter(r[0] for r in rows if r[4] in gset)
    if not codes_for_npi:
        continue
    code, want = codes_for_npi.most_common(1)[0]
    res = api("/api/rates", code=code, npi=npi, page_size=5)
    check(f"npi {npi} + code {code}", res['pagination']['total'], want)
    for r in res['results']:
        if r['provider_group'].get('matched_npi') != npi:
            failures.append(f"npi {npi}: matched_npi not surfaced on row")

# ---- 4. EIN filtering, dashed and bare ---------------------------------------
print("checking EIN filters (bare and dashed)...")
for ein in random.sample(sorted(ein_to_groups), 25):
    gset = ein_to_groups[ein]
    codes_for_ein = collections.Counter(r[0] for r in rows if r[4] in gset)
    if not codes_for_ein:
        continue
    code, want = codes_for_ein.most_common(1)[0]
    check(f"ein {ein} + code {code}", api("/api/rates", code=code, ein=ein, page_size=1)['pagination']['total'], want)
    dashed = ein[:2] + "-" + ein[2:]
    check(f"ein {dashed} (dashed) + code {code}", api("/api/rates", code=code, ein=dashed, page_size=1)['pagination']['total'], want)

# ---- 5. NPI+EIN intersection -------------------------------------------------
print("checking NPI+EIN intersection...")
inter_checked = 0
for npi in random.sample(npi_candidates, 400):
    gset = npi_to_groups[npi]
    gk = next(iter(gset))
    # find the EIN of that same group
    for sid_fn, fn in enumerate(FILES):
        pass
    ein = None
    for e, gs in ein_to_groups.items():
        if gk in gs:
            ein = e
            break
    if not ein:
        continue
    both = gset & ein_to_groups[ein]
    codes_for = collections.Counter(r[0] for r in rows if r[4] in both)
    if not codes_for:
        continue
    code, want = codes_for.most_common(1)[0]
    check(f"npi {npi} + ein {ein} + code {code}", api("/api/rates", code=code, npi=npi, ein=ein, page_size=1)['pagination']['total'], want)
    inter_checked += 1
    if inter_checked >= 10:
        break

# ---- 6. billing_class filter partitions the result set -----------------------
print("checking billing_class filter partitions...")
for code in random.sample(sorted(by_code), 10):
    total = api("/api/rates", code=code, page_size=1)['pagination']['total']
    parts = 0
    for cls in ('professional', 'institutional'):
        parts += api("/api/rates", code=code, billing_class=cls, page_size=1)['pagination']['total']
    check(f"code {code} billing_class parts sum to total", parts, total)

# ---- 7. pagination partitions the result set ---------------------------------
print("checking pagination completeness...")
code = collections.Counter({c: len(v) for c, v in by_code.items()}).most_common(1)[0][0]
total = len(by_code[code])
seen, page = 0, 1
rate_sum_api = 0.0
while True:
    res = api("/api/rates", code=code, page=page, page_size=500, sort="rate_asc")
    if not res['results']:
        break
    seen += len(res['results'])
    rate_sum_api += sum(r['negotiated_rate'] for r in res['results'])
    page += 1
    if page > 500:
        break
check(f"code {code} pagination row count", seen, total)
checkf(f"code {code} pagination rate sum",
       rate_sum_api, sum(sorted(r[2] for r in by_code[code])), )

# ---- 8. sort order -----------------------------------------------------------
print("checking sort order...")
asc = [r['negotiated_rate'] for r in api("/api/rates", code=code, sort="rate_asc", page_size=200)['results']]
desc = [r['negotiated_rate'] for r in api("/api/rates", code=code, sort="rate_desc", page_size=200)['results']]
check("rate_asc is non-decreasing", asc == sorted(asc), True)
check("rate_desc is non-increasing", desc == sorted(desc, reverse=True), True)

print("\n" + "=" * 70)
if failures:
    print(f"{len(failures)} FAILURES")
    for f in failures[:30]:
        print(" -", f)
    sys.exit(1)
print("ALL CROSS-CHECKS PASSED")
