#!/usr/bin/env python3
"""Check that the test suites actually catch the bugs they claim to.

A passing test suite proves nothing on its own: a test that still passes
against deliberately broken code is not protecting anything. This reverts each
real bug that was found and fixed during development, runs the relevant suite,
and asserts the suite fails. Every source file is restored afterwards.

Usage:
    python3 scripts/mutation-check.py            # both suites
    python3 scripts/mutation-check.py --go       # backend only
    python3 scripts/mutation-check.py --vue      # frontend only
"""

from __future__ import annotations

import argparse
import os
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BACKEND = os.path.join(ROOT, "backend")
FRONTEND = os.path.join(ROOT, "frontend")


class Mutation:
    def __init__(self, name, path, original, mutated, cwd, cmd):
        self.name, self.path = name, path
        self.original, self.mutated = original, mutated
        self.cwd, self.cmd = cwd, cmd


GO = lambda pkg: (BACKEND, ["go", "test", f"./internal/{pkg}/", "-count=1"])
VUE = (FRONTEND, ["npx", "vitest", "run", "--silent"])

MUTATIONS = [
    # ---- backend: the three bugs that produced silently wrong prices -------
    Mutation(
        "code summary min/max chained with else-if (reported max == min)",
        "backend/internal/store/builder.go",
        "\t\tr := s.rows[i].Rate\n\t\tif r < a.min {\n\t\t\ta.min = r\n\t\t}\n\t\tif r > a.max {\n\t\t\ta.max = r\n\t\t}",
        "\t\tr := s.rows[i].Rate\n\t\tif r < a.min {\n\t\t\ta.min = r\n\t\t} else if r > a.max {\n\t\t\ta.max = r\n\t\t}",
        *GO("store"),
    ),
    Mutation(
        "CSV export silently clamped to the interactive page size",
        "backend/internal/store/search.go",
        "\tif pageSize > maxPageSize && !q.Unpaged {",
        "\tif pageSize > maxPageSize {",
        *GO("store"),
    ),
    Mutation(
        "unbounded zero-stripping returns a different code's rates",
        "backend/internal/store/search.go",
        "\tswitch len(code) {\n\tcase 4:\n\t\tif code[0] == '0' {\n\t\t\treturn code[1:]\n\t\t}\n\tcase 3:\n\t\treturn \"0\" + code\n\t}\n\treturn \"\"",
        "\tif strings.HasPrefix(code, \"0\") {\n\t\treturn strings.TrimLeft(code, \"0\")\n\t}\n\tif len(code) < 4 {\n\t\treturn \"0\" + code\n\t}\n\treturn \"\"",
        *GO("store"),
    ),
    Mutation(
        "duplicate provider reference duplicates every row",
        "backend/internal/store/builder.go",
        "\texisting := b.refIndex[key]\n\tfor _, o := range ordinals {\n\t\texisting = appendUnique(existing, o)\n\t}\n\tb.refIndex[key] = existing",
        "\tb.refIndex[key] = append(b.refIndex[key], ordinals...)",
        *GO("store"),
    ),
    Mutation(
        "page offset overflows to a negative slice bound",
        "backend/internal/store/search.go",
        "\tstart := len(matched)\n\tif page <= 1 {\n\t\tstart = 0\n\t} else if offset := (page - 1) * pageSize; offset >= 0 && offset < len(matched) {\n\t\tstart = offset\n\t}",
        "\tstart := (page - 1) * pageSize\n\tif start > len(matched) {\n\t\tstart = len(matched)\n\t}",
        *GO("store"),
    ),
    Mutation(
        "float-typed identifiers mangled instead of rejected",
        "backend/internal/tic/innetwork.go",
        "\tif strings.ContainsAny(s, \".eE\") {\n\t\treturn fmt.Errorf(\"not an integer: %s\", s)\n\t}\n",
        "",
        *GO("tic"),
    ),
    Mutation(
        "malformed NPI rejected instead of searchable",
        "backend/internal/tic/identifiers.go",
        "\tif len(d) != 10 {\n\t\tid.Valid = false",
        "\tif len(d) != 10 {\n\t\treturn id, fmt.Errorf(\"bad npi\")\n\t}\n\tif false {\n\t\tid.Valid = false",
        *GO("tic"),
    ),
    Mutation(
        "CSV cells not defused against formula injection",
        "backend/internal/api/handlers.go",
        "\tcase '=', '+', '-', '@', '\\t', '\\r':\n\t\treturn \"'\" + s",
        "\tcase '\\x00':\n\t\treturn \"'\" + s",
        *GO("api"),
    ),
    # ---- frontend ---------------------------------------------------------
    Mutation(
        "javascript: URLs from the payer file rendered as links",
        "frontend/src/composables/useFormat.ts",
        "  return /^https?:\\/\\//i.test(url.trim()) ? url : undefined",
        "  return url",
        *VUE,
    ),
    Mutation(
        "decodeURIComponent unguarded against malformed escapes",
        "frontend/src/composables/useFormat.ts",
        "  try {\n    return decodeURIComponent(url)\n  } catch {\n    return url\n  }",
        "  return decodeURIComponent(url)",
        *VUE,
    ),
    Mutation(
        "single-option facet hidden even when it is the way out",
        "frontend/src/components/FacetFilters.vue",
        "      if (options === 1) return String(props.params[d.key] ?? '') !== ''",
        "      if (options === 1) return false",
        *VUE,
    ),
    Mutation(
        "clearing the code leaves the in-flight request running",
        "frontend/src/composables/useRateSearch.ts",
        "      inFlight?.abort()\n      inFlight = null\n      loading.value = false\n      response.value = null",
        "      response.value = null",
        *VUE,
    ),
    Mutation(
        "typing searches on every keystroke",
        "frontend/src/composables/useRateSearch.ts",
        "  const patch = (next: Partial<SearchParams>) => {\n    params.value = { ...params.value, ...next, page: 1 }\n  }",
        "  const patch = (next: Partial<SearchParams>) => {\n    params.value = { ...params.value, ...next, page: 1 }\n    void run()\n  }",
        *VUE,
    ),
    Mutation(
        "back to a URL with no code leaves stale results on screen",
        "frontend/src/composables/useRateSearch.ts",
        "    params.value = readParamsFromUrl()\n    return run({ pushUrl: false })",
        "    params.value = readParamsFromUrl()\n    return params.value.code.trim() ? run({ pushUrl: false }) : Promise.resolve()",
        *VUE,
    ),
    Mutation(
        "validation errors show the field name as the headline",
        "frontend/src/api/client.ts",
        "    super((looksLikeFieldName ? body.detail : body.error) || body.error || fallback)",
        "    super(body.error || fallback)",
        *VUE,
    ),
    Mutation(
        "row expansion keyed by position and never reset",
        "frontend/src/components/ResultsTable.vue",
        "const rowKey = (row: RateResult, i: number) =>\n  `${row.source.id}:${row.provider_group.provider_group_id}:${row.negotiated_rate}:${i}`",
        "const rowKey = (_row: RateResult, i: number) => `${i}`",
        *VUE,
        # paired below: both mechanisms must go, since either alone masks it
    ),
]

# The expansion bug needs both defences removed to be observable.
PAIRED = {
    "row expansion keyed by position and never reset": (
        "frontend/src/components/ResultsTable.vue",
        "    expanded.value = new Set()\n    copied.value = null",
        "    /* mutation: reset removed */",
    )
}


def run(mutations: list[Mutation]) -> int:
    results = []
    for m in mutations:
        full = os.path.join(ROOT, m.path)

        # Collect every edit this mutation makes, keyed by absolute path, so a
        # mutation that patches one file twice is applied to a single buffer.
        # Restoring per-edit would write the patched copy back over the
        # original when both edits target the same file.
        edits: dict[str, list[tuple[str, str]]] = {full: [(m.original, m.mutated)]}
        if m.name in PAIRED:
            p2, o2, n2 = PAIRED[m.name]
            edits.setdefault(os.path.join(ROOT, p2), []).append((o2, n2))

        originals = {path: open(path).read() for path in edits}
        missing = [
            path for path, pairs in edits.items()
            if any(before not in originals[path] for before, _ in pairs)
        ]
        if missing:
            results.append((m.name, "SKIP", "anchor not found -- update this mutation"))
            continue

        for path, pairs in edits.items():
            buf = originals[path]
            for before, after in pairs:
                buf = buf.replace(before, after, 1)
            open(path, "w").write(buf)

        try:
            proc = subprocess.run(m.cmd, cwd=m.cwd, capture_output=True, text=True, timeout=600)
            results.append((m.name, "CAUGHT" if proc.returncode != 0 else "MISSED", ""))
        except subprocess.TimeoutExpired:
            results.append((m.name, "TIMEOUT", ""))
        finally:
            for path, original in originals.items():
                open(path, "w").write(original)

    print()
    print("=" * 76)
    for name, status, note in results:
        mark = {"CAUGHT": "PASS", "MISSED": "FAIL", "SKIP": "SKIP", "TIMEOUT": "FAIL"}[status]
        print(f"  [{mark}] {name}")
        if note:
            print(f"         {note}")
    print("=" * 76)
    caught = sum(1 for _, s, _ in results if s == "CAUGHT")
    total = len(results)
    print(f"  {caught}/{total} reverted bugs were caught by the test suites")
    if caught != total:
        print("  A MISSED line means the tests would not have stopped that bug returning.")
    return 0 if caught == total else 1


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--go", action="store_true", help="backend mutations only")
    ap.add_argument("--vue", action="store_true", help="frontend mutations only")
    args = ap.parse_args()

    selected = MUTATIONS
    if args.go:
        selected = [m for m in MUTATIONS if m.cwd == BACKEND]
    elif args.vue:
        selected = [m for m in MUTATIONS if m.cwd == FRONTEND]
    return run(selected)


if __name__ == "__main__":
    sys.exit(main())
