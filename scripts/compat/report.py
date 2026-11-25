#!/usr/bin/env python3
"""Turns the raw output of run.sh into results.json and README.md.

    python3 -I scripts/compat/report.py <raw-dir> [--out scripts/compat/out] [--date YYYY-MM-DD]

Nothing is estimated here: every figure is read from what GraphLume printed.
"""
import json
import os
import re
import sys

here = os.path.dirname(os.path.abspath(__file__))


def read(path, default=""):
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            return f.read()
    except OSError:
        return default


def corpus():
    rows = []
    for line in read(os.path.join(here, "corpus.tsv")).splitlines():
        if not line.strip() or line.startswith("#"):
            continue
        f = line.split("\t")
        rows.append(dict(id=f[0], stack=f[1], url=f[2], pin=f[3], why=f[4]))
    return rows


COMPILER = re.compile(r"^  Compiler:\s+(.*)$")
GOTYPES = re.compile(r"^  Go types:\s+(.*)$")


def compilers(out):
    """One entry per compiler line of the resolve report, plus install failures."""
    found = []
    lines = out.splitlines()
    for i, line in enumerate(lines):
        m = COMPILER.match(line)
        if m:
            text = m.group(1)
            if "nested Python project" in text:
                continue
            extra = []
            j = i + 1
            while j < len(lines) and lines[j].startswith("               "):
                extra.append(lines[j].strip())
                j += 1
            if "NOT imported" in text and "nothing to import" in text:
                outcome = "ran, nothing to import"
            elif "NOT imported" in text:
                outcome = "ran, index not imported"
            elif ": not run:" in text:
                outcome = "not run"
            elif "reused its earlier index" in text:
                outcome = "reused"
            elif ": ran in" in text:
                outcome = "ran"
            elif text.startswith("the compiler pass stopped"):
                outcome = "stopped"
            else:
                continue
            tool = text.split(" ")[0]
            n_same = re.search(r"the same for (\d+) projects", text)
            lang = re.search(r"\((\w[\w#+-]*)\)", text)
            reason = ""
            if outcome in ("not run", "stopped", "ran, index not imported", "ran, nothing to import"):
                reason = " ".join([text.split(": ", 2)[-1]] + extra)
            for _ in range(int(n_same.group(1)) if n_same else 1):
                found.append(dict(tool=tool, language=lang.group(1) if lang else "", outcome=outcome,
                                  detail=re.sub(r" \(the same for \d+ projects\)", "", reason).strip(), text=text))
            continue
        m = GOTYPES.match(line)
        if m:
            text = m.group(1)
            outcome = "not run" if text.startswith("not run") else "ran"
            found.append(dict(tool="go/types", language="go", outcome=outcome,
                              detail=text if outcome == "not run" else "", text=text))
    for m in re.finditer(r"could not install (\S+): (.*)", out):
        found.append(dict(tool=m.group(1), language="", outcome="not installed", detail=m.group(2), text=m.group(0)))
    return found


def peak_rss_mb(raw):
    m = re.search(r"(\d+)\s+maximum resident set size", read(os.path.join(raw, "time.txt")))
    return round(int(m.group(1)) / (1024 * 1024)) if m else None


def parse(rawdir, meta):
    raw = os.path.join(rawdir, meta["id"])
    if not os.path.isdir(raw):
        return None
    r = dict(meta)
    if os.path.exists(os.path.join(raw, "clone.failed")):
        r.update(cloned=False, indexed_ok=False, note="the clone failed; see the harness log")
        return r
    out = read(os.path.join(raw, "init.out"))
    status = read(os.path.join(raw, "status.txt"))
    r["cloned"] = True
    r["commit"] = read(os.path.join(raw, "commit.txt")).strip()
    r["tracked_files"] = int(read(os.path.join(raw, "tracked_files.txt"), "0").strip() or 0)
    r["exit_code"] = int(read(os.path.join(raw, "exit_code.txt"), "-1").strip() or -1)
    r["wall_seconds"] = int(read(os.path.join(raw, "wall_seconds.txt"), "0").strip() or 0)
    r["peak_rss_mb"] = peak_rss_mb(raw)
    m = re.search(r"^Files:\s+(\d+)", status, re.M)
    r["files_indexed_total"] = int(m.group(1)) if m else None
    m = re.search(r"^Symbols:\s+(\d+)", status, re.M)
    r["symbols"] = int(m.group(1)) if m else None
    m = re.search(r"^Relationships:\s+(\d+) \(exact compiler-proven: (\d+)", status, re.M)
    if m:
        r["relationships"] = int(m.group(1))
        r["relationships_exact"] = int(m.group(2))
        r["exact_percent"] = round(100.0 * int(m.group(2)) / int(m.group(1)), 1) if int(m.group(1)) else 0.0
    else:
        m = re.search(r"^Relationships:\s+(\d+)", status, re.M)
        r["relationships"] = int(m.group(1)) if m else None
        r["relationships_exact"] = 0
        r["exact_percent"] = 0.0 if m else None
    ready = re.search(r"^Index:\s+READY", status, re.M) is not None
    r["indexed_ok"] = r["exit_code"] == 0 and ready
    r["compilers"] = compilers(out)
    r["install_lines"] = [l.strip() for l in out.splitlines() if l.startswith("  - ")]
    r["stderr_errors"] = [l for l in read(os.path.join(raw, "init.err")).splitlines() if re.search(r"level=(ERROR|WARN)", l)][:5]
    return r


def summarize(r):
    """Per tool: how many projects ran, reused, ran with nothing to import, did not run (and why)."""
    tools = {}
    for c in r.get("compilers", []):
        t = tools.setdefault(c["tool"], {})
        t.setdefault(c["outcome"], []).append(c["detail"])
    return tools


def cell_compilers(r):
    tools = summarize(r)
    if not tools:
        return "none detected"
    parts = []
    for tool, outs in tools.items():
        bits = []
        for outcome, details in outs.items():
            bit = "%d %s" % (len(details), outcome)
            reasons = []
            for d in details:
                d = d.replace("|", "/")
                if d and d not in reasons:
                    reasons.append(d)
            if reasons and outcome in ("not run", "not installed", "stopped", "ran, index not imported"):
                d = reasons[0]
                d = d if len(d) <= 140 else d[:137] + "..."
                bit += " (%s%s)" % (d, "; +%d other reasons" % (len(reasons) - 1) if len(reasons) > 1 else "")
            bits.append(bit)
        parts.append("%s: %s" % (tool, ", ".join(bits)))
    return "<br>".join(parts)


def unclean(results):
    """Every result that is not a plain success, stated plainly."""
    out = []
    for r in results:
        if not r.get("cloned"):
            out.append("%s: the clone failed." % r["id"])
            continue
        if not r["indexed_ok"]:
            out.append("%s: indexing did not complete (exit %s)." % (r["id"], r["exit_code"]))
        for tool, outs in summarize(r).items():
            for outcome, details in outs.items():
                if outcome in ("ran", "reused"):
                    continue
                reasons = sorted(set(d for d in details if d))
                out.append("%s: %s %s for %d project(s)%s" % (
                    r["id"], tool, outcome, len(details),
                    (": " + reasons[0][:300] + (" (and %d other reason(s))" % (len(reasons) - 1) if len(reasons) > 1 else "")) if reasons else "."))
        if r.get("exact_percent") is not None and r["exact_percent"] < 50:
            out.append("%s: only %s%% of relationships are compiler-proven (%s of %s)." % (
                r["id"], r["exact_percent"], r["relationships_exact"], r["relationships"]))
    return out


def main():
    args = sys.argv[1:]
    if not args:
        print(__doc__)
        return 2
    rawdir = args[0]
    outdir = os.path.join(os.path.dirname(here), "..", "docs", "verification", "compat")
    date = ""
    if "--out" in args:
        outdir = args[args.index("--out") + 1]
    if "--date" in args:
        date = args[args.index("--date") + 1]
    results = []
    for meta in corpus():
        r = parse(rawdir, meta)
        if r:
            results.append(r)
    os.makedirs(outdir, exist_ok=True)
    doc = dict(record_date=date, graphlume=read(os.path.join(rawdir, "..", "version.txt")).strip(), results=results)
    with open(os.path.join(outdir, "results.json"), "w") as f:
        json.dump(doc, f, indent=2)
        f.write("\n")
    lines = ["| Repository | Stack | Files | Indexed | Compilers | Exact | Time | Peak RSS |",
             "|---|---|---:|---|---|---:|---:|---:|"]
    for r in results:
        if not r.get("cloned"):
            lines.append("| %s | %s | - | clone failed | - | - | - | - |" % (r["id"], r["stack"]))
            continue
        lines.append("| %s | %s | %s | %s | %s | %s | %ss | %s |" % (
            r["id"], r["stack"], r["tracked_files"],
            "yes" if r["indexed_ok"] else "NO (exit %s)" % r["exit_code"],
            cell_compilers(r),
            ("%s%%" % r["exact_percent"]) if r.get("exact_percent") is not None else "-",
            r["wall_seconds"],
            ("%d MB" % r["peak_rss_mb"]) if r.get("peak_rss_mb") else "-"))
    readme = ["# Compatibility record", "",
              "GraphLume run cold, the way a new user would, on %d public repositories pinned to a tag or commit. "
              "Each run is `graphlume init --index --indexers` in a fresh shallow clone, with a throwaway home directory "
              "and a PATH that holds the language toolchains but none of the compiler indexers, so every indexer is "
              "downloaded by GraphLume itself. No agent registration, no hand fixes." % len(results),
              "",
              "Reproduce with `scripts/compat/run.sh` then `python3 -I scripts/compat/report.py <raw dir>`. "
              "The corpus, with the reason each repository is in it, is `scripts/compat/corpus.tsv`; the "
              "machine-readable record is `results.json` beside this file.", ""]
    if date:
        readme += ["Recorded %s, GraphLume %s." % (date, doc["graphlume"] or "development build"), ""]
    readme += ["**Exact** is the share of the graph's relationships a compiler proved (`graphlume status`). "
               "**Files** is the number of files git tracks at the pin. Time is wall clock for the one command, "
               "including the first-time download of the indexers it needed; the first repository to need an "
               "indexer pays for it. Peak RSS is the largest resident set of any process in the run.", ""]
    readme += lines + [""]
    problems = unclean(results)
    readme += ["## Results that are not clean", ""]
    readme += ["- " + p for p in problems] if problems else ["None."]
    readme += [""]
    notes = read(os.path.join(here, "notes.md")).strip()
    if notes:
        readme += [notes, ""]
    with open(os.path.join(outdir, "README.md"), "w") as f:
        f.write("\n".join(readme))
    print("\n".join(lines))
    return 0


if __name__ == "__main__":
    sys.exit(main())
