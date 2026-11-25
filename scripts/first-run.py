#!/usr/bin/env python3
"""Run GraphLume the way a new user does and check every case in
docs/operations/release-checklist.md that a script can check.

Usage: python3 scripts/first-run.py [graphlume-binary] [--only A1,B2,...]

Every case runs with HOME pointed at a throwaway directory, so nothing here
touches your own agent configs. Interactive cases run in a real
pseudo-terminal. Needs git; some cases need network access.
Exit status: 0 when every case passes, 1 otherwise.
"""
import json
import os
import pty
import re
import select
import shutil
import signal
import subprocess
import sys
import tempfile
import time

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SILENCE = float(os.environ.get("SILENCE", "20"))
REPEAT = int(os.environ.get("REPEAT", "3"))
QUESTION = re.compile(r"\[(Y/n|y/N)\]")
ESC = "\x1b["

results = []  # (id, ok, detail)


def record(cid, ok, detail=""):
    results.append((cid, ok, detail))
    mark = "\033[32mok  \033[0m" if ok else "\033[31mFAIL\033[0m"
    print(f"  {mark} {cid}  {detail}", flush=True)


class Run:
    """One finished command: exit code, output lines with times, questions."""

    def __init__(self, code, lines):
        self.code = code
        self.lines = lines  # [(seconds, text)]
        self.text = "\n".join(t for _, t in lines)

    def longest_silence(self):
        gaps = [b[0] - a[0] for a, b in zip(self.lines, self.lines[1:])]
        return max(gaps, default=0.0)

    def repeated(self):
        seen = {}
        for _, t in self.lines:
            t = t.strip()
            if t:
                seen[t] = seen.get(t, 0) + 1
        worst = max(seen.items(), key=lambda kv: kv[1], default=("", 0))
        return worst if worst[1] > REPEAT else None

    def questions(self):
        return [t for _, t in self.lines if QUESTION.search(t)]


def run_plain(gl, cwd, args, env, timeout=900):
    """Run without a terminal, stdin closed."""
    start = time.time()
    p = subprocess.Popen([gl] + args, cwd=cwd, env=env, stdin=subprocess.DEVNULL,
                         stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, errors="replace")
    lines = []
    try:
        for line in p.stdout:
            lines.append((time.time() - start, line.rstrip("\n")))
            if time.time() - start > timeout:
                p.kill()
                lines.append((time.time() - start, f"<<killed after {timeout}s>>"))
                break
        p.wait(timeout=30)
    except subprocess.TimeoutExpired:
        p.kill()
    return Run(p.returncode, lines)


def run_tty(gl, cwd, args, env, answer="n", interrupt_at_question=False, timeout=900):
    """Run in a pseudo-terminal, answering every question with `answer`."""
    pid, fd = pty.fork()
    if pid == 0:
        os.chdir(cwd)
        os.execve(gl, [gl] + args, env)
    start = time.time()
    buf, lines = "", []
    code = None
    while True:
        if time.time() - start > timeout:
            os.kill(pid, signal.SIGKILL)
            lines.append((time.time() - start, f"<<killed after {timeout}s>>"))
            break
        r, _, _ = select.select([fd], [], [], 0.5)
        if r:
            try:
                chunk = os.read(fd, 4096).decode("utf-8", "replace")
            except OSError:
                chunk = ""
            if not chunk:
                break
            buf += chunk
            parts = re.split(r"\r\n|\n|\r", buf)
            buf = parts.pop()
            for part in parts:
                lines.append((time.time() - start, part))
            if QUESTION.search(buf):
                lines.append((time.time() - start, buf))
                buf = ""
                if interrupt_at_question:
                    os.write(fd, b"\x03")
                    time.sleep(0.5)
                    os.kill(pid, signal.SIGINT)
                else:
                    os.write(fd, (answer + "\r").encode())
        else:
            done, status = os.waitpid(pid, os.WNOHANG)
            if done:
                code = os.waitstatus_to_exitcode(status)
                break
    if buf:
        lines.append((time.time() - start, buf))
    if code is None:
        try:
            _, status = os.waitpid(pid, 0)
            code = os.waitstatus_to_exitcode(status)
        except ChildProcessError:
            code = -1
    os.close(fd)
    return Run(code, lines)


def common_checks(cid, run):
    """The checks every journey shares: silence, repetition, surprise downloads."""
    ok = True
    if run.longest_silence() > SILENCE:
        record(cid + "/silence", False, f"{run.longest_silence():.0f}s without output")
        ok = False
    rep = run.repeated()
    if rep:
        record(cid + "/repeat", False, f"{rep[1]}x: {rep[0][:90]}")
        ok = False
    logs = [t for _, t in run.lines if re.match(r"\s*time=\S+ level=(INFO|DEBUG)", t)]
    if logs:
        record(cid + "/logs", False, f"{len(logs)} internal log line(s), e.g. {logs[0][:80]}")
        ok = False
    if "uncommitted changes" in run.text:
        record(cid + "/own-files", False, "warns about uncommitted changes GraphLume made itself")
        ok = False
    # Within one phase (phases start at "==>" headers), each progress mark once.
    dup_pct = set()
    for phase in re.split(r"\n\s*==>", run.text):
        pcts = re.findall(r"(\d+)% \(", phase)
        dup_pct |= {p for p in pcts if pcts.count(p) > 1}
    if dup_pct:
        record(cid + "/progress", False, f"progress printed twice in one phase for {sorted(dup_pct, key=int)}%")
        ok = False
    if "could not be prepared" in run.text and "Failed:" in run.text:
        record(cid + "/failure-twice", False, "the same setup failure is reported twice")
        ok = False
    if re.search(r"go: downloading go\d", run.text):
        record(cid + "/download", False, "go downloaded a toolchain")
        ok = False
    return ok


class Env:
    def __init__(self, work, name, fresh_machine=False):
        self.home = os.path.join(work, "home-" + name)
        os.makedirs(os.path.join(self.home, ".config"))
        with open(os.path.join(self.home, ".claude.json"), "w") as f:
            json.dump({"mcpServers": {"other": {"command": "other-server"}}}, f)
        self.env = dict(os.environ, HOME=self.home, USERPROFILE=self.home,
                        XDG_CONFIG_HOME=os.path.join(self.home, ".config"))
        self.env.pop("GOTOOLCHAIN", None)
        if fresh_machine:
            # The toolchains a developer has (node, npm, go) but none of the
            # compiler indexers, so every indexer is offered and asked about.
            bin_ = os.path.join(self.home, "toolchains")
            os.makedirs(bin_)
            for tool in ("node", "npm", "go"):
                found = shutil.which(tool)
                if found:
                    os.symlink(os.path.realpath(found), os.path.join(bin_, tool))
            self.env["PATH"] = bin_ + ":/usr/bin:/bin:/usr/sbin:/sbin"
            self.env.pop("GOPATH", None)

    def claude(self):
        with open(os.path.join(self.home, ".claude.json")) as f:
            return json.load(f).get("mcpServers", {})


def git_init(d):
    subprocess.run(["git", "init", "-q"], cwd=d, check=True)
    subprocess.run(["git", "add", "-A"], cwd=d, check=True)
    subprocess.run(["git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "init"], cwd=d, check=True)


def sample(work, name, git=True):
    d = os.path.join(work, name)
    shutil.copytree(os.path.join(REPO, "fixtures", "payment-platform"), d, symlinks=True)
    shutil.rmtree(os.path.join(d, ".graphlume"), ignore_errors=True)
    if git:
        git_init(d)
    return d


def monorepo(work, name):
    d = os.path.join(work, name)
    for i in range(3):
        s = os.path.join(d, f"web{i}", "src")
        os.makedirs(s)
        open(os.path.join(d, f"web{i}", "package.json"), "w").write(json.dumps({"name": f"web{i}", "version": "1.0.0"}))
        open(os.path.join(d, f"web{i}", "tsconfig.json"), "w").write('{"compilerOptions":{"strict":true}}')
        open(os.path.join(s, "index.ts"), "w").write(f"export function f{i}(): number {{ return {i} }}\n")
    for i in range(3):
        s = os.path.join(d, f"py{i}")
        os.makedirs(s)
        open(os.path.join(s, "pyproject.toml"), "w").write(f'[project]\nname = "py{i}"\nversion = "1"\n')
        open(os.path.join(s, "app.py"), "w").write(f"def g{i}():\n    return {i}\n")
    for i in range(3):
        s = os.path.join(d, f"go{i}")
        os.makedirs(s)
        open(os.path.join(s, "go.mod"), "w").write(f"module example.com/go{i}\n\ngo 1.21\n")
        open(os.path.join(s, "a.go"), "w").write(f"package go{i}\n\nfunc H() int {{ return {i} }}\n")
    git_init(d)
    return d


def main():
    args = [a for a in sys.argv[1:] if not a.startswith("--only")]
    only = None
    for a in sys.argv[1:]:
        if a.startswith("--only"):
            only = set(a.split("=", 1)[1].split(","))
    work = tempfile.mkdtemp(prefix="graphlume-first-run-")
    gl = os.path.abspath(args[0]) if args else os.path.join(work, "graphlume")
    if not args:
        subprocess.run(["go", "build", "-o", gl, "./cmd/graphlume"], cwd=REPO, check=True,
                       env=dict(os.environ, CGO_ENABLED="0"))
    if subprocess.run([gl, "version"], capture_output=True).returncode != 0:
        sys.exit(f"first-run: cannot run {gl}")
    print(f"binary: {gl}\nwork:   {work}\n")

    def want(cid):
        return only is None or cid in only

    # ---------------------------------------------------------------- A
    print("A. Where it is run")
    if want("A1"):
        e = Env(work, "a1")
        r = run_plain(gl, e.home, ["init", "--all", "--yes"], e.env)
        extra = [n for n in os.listdir(e.home) if n not in (".config", ".claude.json")]
        record("A1", r.code != 0 and "your home directory, not a project" in r.text and not extra
               and e.claude().keys() == {"other"}, f"exit {r.code}; wrote {extra or 'nothing'}")
    if want("A2"):
        e = Env(work, "a2")
        r = run_plain(gl, "/", ["init", "--all", "--yes"], e.env)
        record("A2", r.code != 0 and "root of the filesystem, not a project" in r.text, f"exit {r.code}")
    if want("A3"):
        e = Env(work, "a3")
        d = sample(work, "a3")
        sub = next(os.path.join(d, n) for n in sorted(os.listdir(d))
                   if not n.startswith(".") and os.path.isdir(os.path.join(d, n)))
        r = run_plain(gl, sub, ["init", "--all", "--yes"], e.env)
        common_checks("A3", r)
        record("A3", r.code == 0 and d in r.text, "names the enclosing repository" if d in r.text else "does not say it is part of a repository")
    if want("A4"):
        e = Env(work, "a4")
        d = sample(work, "a4", git=False)
        r = run_plain(gl, d, ["init", "--all", "--yes"], e.env)
        common_checks("A4", r)
        record("A4", r.code == 0 and re.search(r"not a git repository|no git history", r.text, re.I) is not None,
               f"exit {r.code}; mentions git: {bool(re.search(r'git', r.text, re.I))}")
    if want("A5"):
        e = Env(work, "a5")
        d = os.path.join(work, "a5")
        os.makedirs(d)
        r = run_plain(gl, d, ["init", "--all", "--yes"], e.env)
        common_checks("A5", r)
        record("A5", r.code == 0 and re.search(r"no source files", r.text, re.I) is not None, f"exit {r.code}")
    if want("A6"):
        e = Env(work, "a6", fresh_machine=True)
        d = monorepo(work, "a6")
        r = run_tty(gl, d, ["init", "--all"], e.env, answer="n")
        common_checks("A6", r)
        qs = r.questions()
        dup = len(qs) != len(set(q.strip() for q in qs))
        record("A6", r.code == 0 and not dup and len(qs) <= 4, f"exit {r.code}; {len(qs)} question(s): {[q.strip()[:60] for q in qs]}")
    if want("A6b"):
        # The same monorepo on a machine where the indexers are installed:
        # each project is indexed, and the summary must not repeat per project.
        e = Env(work, "a6b")
        d = monorepo(work, "a6b")
        r = run_plain(gl, d, ["init", "--all", "--yes"], e.env)
        ok = common_checks("A6b", r)
        record("A6b", r.code == 0 and ok, f"exit {r.code}")
    if want("A7"):
        e = Env(work, "a7")
        d = sample(work, "a7")
        os.chmod(d, 0o555)
        r = run_plain(gl, d, ["init", "--all", "--yes"], e.env)
        os.chmod(d, 0o755)
        record("A7", r.code != 0 and d in r.text and "panic" not in r.text, f"exit {r.code}: {r.lines[-1][1][:100] if r.lines else ''}")
    if want("A8"):
        e = Env(work, "a8")
        d = sample(work, "a8 sërvice dir")
        r = run_plain(gl, d, ["init", "--all", "--yes"], e.env)
        st = run_plain(gl, d, ["status"], e.env)
        record("A8", r.code == 0 and "READY" in st.text, f"exit {r.code}")

    # ---------------------------------------------------------------- B
    print("B. Running it again")
    if want("B1") or want("B2"):
        e = Env(work, "b1")
        d = sample(work, "b1")
        run_plain(gl, d, ["init", "--all", "--yes"], e.env)
        if want("B1"):
            r = run_plain(gl, d, ["init", "--all", "--yes"], e.env)
            common_checks("B1", r)
            record("B1", r.code == 0, f"exit {r.code}: {r.lines[-1][1][:110] if r.lines else ''}")
        if want("B2"):
            r = run_plain(gl, d, ["init"], e.env)
            record("B2", r.code == 0 and "already" in r.text.lower(), f"exit {r.code}: {r.text[:110]}")
    if want("B3"):
        e = Env(work, "b3")
        d = sample(work, "b3")
        run_plain(gl, d, ["init", "--all", "--yes"], e.env)
        p = subprocess.Popen([gl, "index", "--full"], cwd=d, env=e.env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        time.sleep(0.3)
        p.send_signal(signal.SIGINT)
        p.wait(timeout=60)
        st = run_plain(gl, d, ["status"], e.env)
        again = run_plain(gl, d, ["index"], e.env)
        record("B3", "READY" in st.text and again.code == 0, f"status after Ctrl-C: {'READY' if 'READY' in st.text else st.text[:80]}")
    if want("B4"):
        e = Env(work, "b4")
        d = sample(work, "b4")
        run_plain(gl, d, ["init", "--all", "--yes"], e.env)
        run_plain(gl, d, ["clean", "--force"], e.env)
        r = run_plain(gl, d, ["init", "--all", "--yes"], e.env)
        record("B4", r.code == 0, f"exit {r.code}")

    # ---------------------------------------------------------------- C
    print("C. Questions")
    if want("C1") or want("C2"):
        e = Env(work, "c1", fresh_machine=True)
        d = monorepo(work, "c1")
        r1 = run_tty(gl, d, ["init", "--all"], e.env, answer="n")
        qs = [q.strip() for q in r1.questions()]
        if want("C1"):
            per_lang = {}
            for q in qs:
                per_lang[q] = per_lang.get(q, 0) + 1
            record("C1", all(v == 1 for v in per_lang.values()), f"{len(qs)} question(s): {qs}")
        if want("C2"):
            r2 = run_tty(gl, d, ["init", "--all"], e.env, answer="n")
            record("C2", len(r2.questions()) == 0, f"second run asked {len(r2.questions())}: {[q.strip()[:60] for q in r2.questions()]}")
    if want("C3"):
        e = Env(work, "c3", fresh_machine=True)
        d = monorepo(work, "c3")
        r = run_plain(gl, d, ["init", "--all"], e.env, timeout=300)
        record("C3", r.code == 0 and "<<killed" not in r.text, f"exit {r.code}")
    if want("C5"):
        e = Env(work, "c5", fresh_machine=True)
        d = monorepo(work, "c5")
        r = run_tty(gl, d, ["init", "--all"], e.env, interrupt_at_question=True, timeout=120)
        record("C5", "<<killed" not in r.text and "panic" not in r.text, f"exit {r.code}")

    # ---------------------------------------------------------------- D
    print("D. Output")
    if want("D3") or want("D4") or want("D5"):
        e = Env(work, "d")
        d = sample(work, "d")
        r = run_plain(gl, d, ["init", "--all", "--yes"], e.env)
        common_checks("D", r)
        if want("D3"):
            told = "install --list" in r.text
            lst = run_plain(gl, d, ["install", "--list"], e.env)
            record("D3", told and ".claude.json" in lst.text,
                   "summary points to install --list, which names each file" if told else "summary does not say where the changed files are listed")
        if want("D4"):
            record("D4", ESC not in r.text, "no escape codes without a terminal" if ESC not in r.text else "escape codes in a log")
            q = run_plain(gl, d, ["--quiet", "index"], e.env)
            record("D4/quiet", q.code == 0 and len([t for _, t in q.lines if t.strip()]) <= 1, f"{len(q.lines)} line(s) with --quiet")
        if want("D5"):
            n = len(re.findall(r"Next Steps|Next steps", r.text))
            record("D5", n <= 1, f"next-steps block shown {n} time(s)")

    # ---------------------------------------------------------------- E
    print("E. Downloads")
    if want("E2"):
        e = Env(work, "e2")
        d = os.path.join(work, "e2")
        os.makedirs(d)
        open(os.path.join(d, "go.mod"), "w").write("module example.com/e2\n\ngo 1.99\n")
        open(os.path.join(d, "a.go"), "w").write("package e2\n\nfunc F() {}\n")
        git_init(d)
        r = run_plain(gl, d, ["init", "--all", "--yes"], e.env)
        silent = re.search(r"go: downloading go1\.99", r.text) is not None
        record("E2", not silent, "no Go release downloaded" if not silent else "go downloaded Go 1.99 without asking")
    if want("E4"):
        # Go read by the built-in type checker runs no go command, so no Go
        # release may be announced as a download.
        e = Env(work, "e4")
        d = os.path.join(work, "e4")
        os.makedirs(d)
        open(os.path.join(d, "go.mod"), "w").write("module example.com/e4\n\ngo 1.99\n")
        open(os.path.join(d, "a.go"), "w").write("package e4\n\nfunc F() {}\n")
        git_init(d)
        r = run_plain(gl, d, ["init", "--all", "--yes"], e.env)
        plan = run_plain(gl, d, ["indexers", "list"], e.env)
        uses_scip_go = re.search(r"^go\s+scip-go\s", plan.text, re.M) is not None
        announced = "the go command downloads" in r.text
        record("E4", announced == uses_scip_go, f"scip-go planned: {uses_scip_go}; download announced: {announced}")
    if want("E3"):
        e = Env(work, "e3")
        d = sample(work, "e3")
        env = dict(e.env, HTTPS_PROXY="http://127.0.0.1:9", HTTP_PROXY="http://127.0.0.1:9", GOPROXY="off")
        r = run_plain(gl, d, ["init", "--all", "--yes"], env, timeout=600)
        st = run_plain(gl, d, ["status"], e.env)
        common_checks("E3", r)
        record("E3", r.code == 0 and "READY" in st.text, f"exit {r.code}; index {'READY' if 'READY' in st.text else 'not ready'}")

    # ---------------------------------------------------------------- G
    print("G. Agent registration")
    if want("G1") or want("G2") or want("G3"):
        # A user sets up six projects, one after another, on one machine.
        e = Env(work, "g")
        projects = [sample(work, f"g-project-{i}") for i in range(6)]
        for d in projects:
            run_plain(gl, d, ["init", "--all", "--yes"], e.env)
        reg = e.claude().get("graphlume", {})
        args_ = " ".join(reg.get("args", []))
        if want("G1"):
            pinned = [os.path.basename(d) for d in projects if d in args_]
            record("G1", not pinned, f"user-level registration is pinned to {pinned or 'no project'}")
            wrong = []
            for d in projects:
                mcp = os.path.join(d, ".mcp.json")
                if not os.path.exists(mcp):
                    wrong.append(os.path.basename(d) + ": no .mcp.json")
                    continue
                a2 = " ".join(json.load(open(mcp)).get("mcpServers", {}).get("graphlume", {}).get("args", []))
                others = [o for o in projects if o != d and o in a2]
                if others:
                    wrong.append(os.path.basename(d) + " -> " + os.path.basename(others[0]))
            record("G1/project", not wrong, f"project registrations wrong: {wrong or 'none'}")
        if want("G2"):
            servers = e.claude()
            record("G2", set(servers) == {"other", "graphlume"}, f"servers after six setups: {sorted(servers)}")
        if want("G3"):
            run_plain(gl, projects[0], ["clean", "--agents", "--force"], e.env)
            record("G3", set(e.claude()) == {"other"}, f"after clean --agents: {sorted(e.claude())}")

    # ---------------------------------------------------------------- R
    print("R. Index health and repair")
    if want("R1") or want("R2"):
        e = Env(work, "r1")
        d = sample(work, "r1")
        run_plain(gl, d, ["init", "--all", "--yes"], e.env)
        src = next(os.path.join(r, f) for r, _, fs in os.walk(d) if ".git" not in r and ".graphlume" not in r
                   for f in fs if f.endswith((".go", ".ts", ".py", ".java")))
        with open(src, "a") as f:
            f.write("\n// changed\n")
        st = run_plain(gl, d, ["status"], e.env)
        if want("R1"):
            record("R1", re.search(r"changed since|out of date|stale", st.text, re.I) is not None, "status says files changed" if re.search(r"changed since|out of date|stale", st.text, re.I) else "status does not say files changed")
        if want("R2"):
            record("R2", re.search(r"hooks install|graphlume watch|kept current", st.text, re.I) is not None, "status says how to keep it current" if re.search(r"hooks install|graphlume watch|kept current", st.text, re.I) else "status does not say how the index is kept current")
    if want("R3"):
        e = Env(work, "r3")
        d = sample(work, "r3")
        run_plain(gl, d, ["init", "--all", "--yes"], e.env)
        db = os.path.join(d, ".graphlume", "index.db")
        with open(db, "r+b") as f:
            f.seek(4096)
            f.write(os.urandom(8192))
        doc = run_plain(gl, d, ["doctor", "--verify-index"], e.env)
        rep = run_plain(gl, d, ["init", "--all", "--yes"], e.env)
        st = run_plain(gl, d, ["status"], e.env)
        record("R3", doc.code != 0 and "READY" in st.text,
               f"doctor exit {doc.code}; init --all exit {rep.code}; after: {'READY' if 'READY' in st.text else st.text[:80]}")
    if want("R5"):
        e = Env(work, "r5")
        d = os.path.join(work, "r5")
        shutil.copytree(REPO, d, ignore=shutil.ignore_patterns(".git", ".graphlume", "fixtures"))
        git_init(d)
        run_plain(gl, d, ["init"], e.env)
        p = subprocess.Popen([gl, "index"], cwd=d, env=e.env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        time.sleep(3)
        p.kill()
        p.wait()
        r = run_plain(gl, d, ["index"], e.env, timeout=600)
        record("R5", r.code == 0, f"index after a killed run: exit {r.code}: {r.lines[-1][1][:100] if r.lines else ''}")

    # ---------------------------------------------------------------- P
    print("P. Platforms")
    if want("P1"):
        src = open(os.path.join(REPO, "install.sh")).read()
        record("P1", "proc_translated" in src, "installer detects Rosetta" if "proc_translated" in src else "installer trusts uname -m, which reports x86_64 under Rosetta")

    failed = [c for c, ok, _ in results if not ok]
    print(f"\n{len(results) - len(failed)} passed, {len(failed)} failed: {', '.join(failed) or 'none'}")
    if not os.environ.get("KEEP"):
        subprocess.run(["chmod", "-R", "u+w", work])
        shutil.rmtree(work, ignore_errors=True)
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
