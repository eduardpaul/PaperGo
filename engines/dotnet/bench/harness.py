#!/usr/bin/env python3
"""Parity and benchmark harness: the .NET engine against the Go engines.

Run with python3 -I (stdlib only). Steps run in order; each writes
OUT/<step>.json. The Go side runs the reference CLIs (cmd/ocr, cmd/embed,
cmd/decide) and goref (bench/goref) for what they don't print. Model
processes never run at the same time, and heavy steps wait until the
machine has at least --min-free-gb of available memory.

  python3 -I engines/dotnet/bench/harness.py --out .cache/bench build ocr tokenizers embeddings decisions bench
"""

import argparse
import difflib
import hashlib
import json
import math
import os
import shutil
import statistics
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.abspath(os.path.join(HERE, "../../.."))
sys.path.insert(0, HERE)
import corpus  # noqa: E402
from engine_client import Engine, EngineError  # noqa: E402

GEMMA, HARRIER = "embeddinggemma-2", "harrier-oss-v1-0.6b"
MAX_TOKENS = 512


def parse_args():
    main_repo = REPO
    # A worktree has no models or native libraries; fall back to the main checkout.
    for candidate in (REPO, os.path.expanduser("~/repos/PaperGo")):
        if os.path.isdir(os.path.join(candidate, ".tools/native/lib")):
            main_repo = candidate
            break
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("steps", nargs="+", choices=["build", "ocr", "tokenizers", "embeddings", "decisions", "bench"])
    p.add_argument("--out", required=True)
    p.add_argument("--engine", default=os.path.join(REPO, "engines/dotnet/out/papergo-engine"))
    p.add_argument("--engine-static", help="an engine published with -p:OnnxRuntimeStatic (OCR parity, size, start and OCR timings)")
    p.add_argument("--go-bin", help="directory of the Go ocr, embed, decide and goref binaries (default OUT/go)")
    p.add_argument("--native", default=os.path.join(main_repo, ".tools/native"))
    p.add_argument("--ocr-models", default=os.path.join(main_repo, "models/paddleocr"))
    p.add_argument("--assets", default=os.path.join(main_repo, "bin/assets/models"))
    p.add_argument("--pages", nargs="*", default=[], help="page images for OCR parity and benchmarks")
    p.add_argument("--threads", type=int, default=1)
    p.add_argument("--runs", type=int, default=3)
    p.add_argument("--min-free-gb", type=float, default=3.0)
    p.add_argument("--max-load", type=float, default=1.5, help="bench waits until the 1-minute load average is below this")
    a = p.parse_args()
    a.out = os.path.abspath(a.out)
    a.go_bin = os.path.abspath(a.go_bin or os.path.join(a.out, "go"))
    a.laya = os.path.join(a.assets, "laya")
    a.embedding = {m: os.path.join(a.assets, "embedding", m) for m in (GEMMA, HARRIER)}
    return a


A = None


def log(*args):
    print(time.strftime("%H:%M:%S"), *args, flush=True)


def save(name, data):
    with open(os.path.join(A.out, name + ".json"), "w") as f:
        json.dump(data, f, indent=1, ensure_ascii=False)


def load(name):
    path = os.path.join(A.out, name + ".json")
    if os.path.exists(path):
        with open(path) as f:
            return json.load(f)
    return None


def go_env():
    env = dict(os.environ)
    lib = os.path.join(A.native, "lib")
    env["LD_LIBRARY_PATH"] = lib + (":" + env["LD_LIBRARY_PATH"] if env.get("LD_LIBRARY_PATH") else "")
    env["ONNXRUNTIME_LIB"] = os.path.join(lib, "libonnxruntime.so")
    return env


def go(tool, *args, stdin=None, check=True):
    r = subprocess.run([os.path.join(A.go_bin, tool), *args], input=stdin, capture_output=True, env=go_env())
    if check and r.returncode != 0:
        raise RuntimeError(f"{tool} {' '.join(args)}: {r.stderr.decode(errors='replace')}")
    return r


def net(*args, stdin=None):
    r = subprocess.run([A.engine, *args], input=stdin, capture_output=True)
    if r.returncode != 0:
        raise RuntimeError(f"papergo-engine {' '.join(args)}: {r.stderr.decode(errors='replace')}")
    return r


def available_gb():
    with open("/proc/meminfo") as f:
        for line in f:
            if line.startswith("MemAvailable:"):
                return int(line.split()[1]) / 1024 / 1024
    return 0


def wait_for_memory(what):
    """Waits until --min-free-gb is available, up to 15 minutes, and, while
    benchmarking, for a quiet machine."""
    if A.benchmarking:
        wait_for_quiet(what)
    deadline = time.monotonic() + 900
    while (gb := available_gb()) < A.min_free_gb:
        if time.monotonic() > deadline:
            raise RuntimeError(f"only {gb:.1f} GB available for {what}")
        log(f"waiting for memory before {what}: {gb:.1f} GB available")
        time.sleep(20)


def socket_path():
    # Unix socket paths are limited to 108 bytes; the runtime directory is short.
    base = os.environ.get("XDG_RUNTIME_DIR") if os.path.isdir(os.environ.get("XDG_RUNTIME_DIR", "")) else A.tmp
    return os.path.join(base, f"papergo-engine-bench-{os.getpid()}.sock")


def wait_for_quiet(what):
    """Waits until the 1-minute load average is below --max-load, up to 30 minutes."""
    deadline = time.monotonic() + 1800
    while (load1 := os.getloadavg()[0]) > A.max_load:
        if time.monotonic() > deadline:
            log(f"measuring {what} under load {load1:.1f}")
            break
        log(f"waiting for a quiet machine before {what}: load {load1:.1f}")
        time.sleep(30)
    A.loads.append((what, round(os.getloadavg()[0], 2)))


def start_engine(*args, idle=None, exe=None):
    sock = socket_path()
    extra = ["--idle-seconds", str(idle)] if idle else []
    e = Engine(exe or A.engine, sock, ["--ocr-threads", str(A.threads), "--embeddings-threads", str(A.threads),
                                "--decision-threads", str(A.threads), *extra, *args])
    e.wait_ready()
    return e


def ocr_args():
    return ["--ocr-models", A.ocr_models]


def embedding_args(*models):
    return ["--embeddings-models", ",".join(f"{m}={A.embedding[m]}" for m in models)]


def laya_args():
    return ["--decision-models", A.laya]


def median_range(values):
    if not values:
        return None
    return {"median": statistics.median(values), "min": min(values), "max": max(values), "n": len(values)}


# ---------------------------------------------------------------- build

def step_build():
    os.makedirs(A.go_bin, exist_ok=True)
    env_sh = os.path.join(A.native, "env.sh")
    script = f". '{env_sh}' && export GOTMPDIR='{A.tmp}' TMPDIR='{A.tmp}' && cd '{REPO}' && " + " && ".join(
        f"go build -p 2 -o '{A.go_bin}/{c}' ./cmd/{c}" for c in ("ocr", "embed", "decide"))
    script += f" && cd '{HERE}/goref' && go build -p 2 -o '{A.go_bin}/goref' ."
    log("building the Go reference CLIs and goref")
    subprocess.run(["bash", "-c", script], check=True)
    fixtures = os.path.join(A.out, "fixtures")
    os.makedirs(fixtures, exist_ok=True)
    go("goref", "fixtures", fixtures)
    log("built", sorted(os.listdir(A.go_bin)), "and fixtures", sorted(os.listdir(fixtures)))


def images():
    fixtures = os.path.join(A.out, "fixtures")
    out = [(f"page{i}", p) for i, p in enumerate(A.pages)]
    out += [(os.path.splitext(f)[0], os.path.join(fixtures, f)) for f in sorted(os.listdir(fixtures))]
    return out


def raw(path):
    dst = os.path.join(A.tmp, "image.raw")
    meta = json.loads(go("goref", "raw", path, dst).stdout)
    with open(dst, "rb") as f:
        return f.read(), meta


# ---------------------------------------------------------------- ocr

def quad_diff(a, b):
    return max(max(abs(p["X"] - q["X"]), abs(p["Y"] - q["Y"])) for p, q in zip(a, b))


def compare_pages(go_page, net_page):
    a, b = go_page["lines"], net_page["lines"]
    r = {"go_lines": len(a), "net_lines": len(b),
         "texts_identical": [x["text"] for x in a] == [y["text"] for y in b],
         "max_quad_px": 0, "max_word_quad_px": 0, "max_confidence_diff": 0.0, "word_counts_equal": True}
    for x, y in zip(a, b):
        r["max_quad_px"] = max(r["max_quad_px"], quad_diff(x["quad"], y["quad"]))
        r["max_confidence_diff"] = max(r["max_confidence_diff"], abs(x["confidence"] - y["confidence"]))
        wx, wy = x["words"] or [], y["words"] or []
        if len(wx) != len(wy):
            r["word_counts_equal"] = False
        for u, v in zip(wx, wy):
            r["max_word_quad_px"] = max(r["max_word_quad_px"], quad_diff(u["quad"], v["quad"]))
    if not r["texts_identical"]:
        ga, nb = "\n".join(x["text"] for x in a), "\n".join(y["text"] for y in b)
        r["char_ratio"] = difflib.SequenceMatcher(None, ga, nb).ratio()
        r["diff"] = list(difflib.unified_diff(ga.splitlines(), nb.splitlines(), "go", "dotnet", lineterm="", n=0))[:40]
    r["pass"] = (r["texts_identical"] and r["go_lines"] == r["net_lines"] and r["max_quad_px"] <= 1
                 and r["max_confidence_diff"] <= 1e-3 and r["word_counts_equal"])
    return r


def compare_detections(g, n):
    r = {"go_regions": len(g["regions"]), "net_regions": len(n["regions"]),
         "preview_equal": (g["width"], g["height"]) == (n["width"], n["height"]), "max_quad_px": 0, "max_score_diff": 0.0}
    for x, y in zip(g["regions"], n["regions"]):
        r["max_quad_px"] = max(r["max_quad_px"], quad_diff(x["quad"], y["quad"]))
        r["max_score_diff"] = max(r["max_score_diff"], abs(x["score"] - y["score"]))
    r["pass"] = r["go_regions"] == r["net_regions"] and r["preview_equal"] and r["max_quad_px"] <= 1 and r["max_score_diff"] <= 1e-3
    return r


def step_ocr():
    gocache = os.path.join(A.out, "go-ocr")
    os.makedirs(gocache, exist_ok=True)
    wait_for_memory("Go OCR")
    golden = {}
    for name, path in images():
        out = go("ocr", "-models", A.ocr_models, "-threads", str(A.threads), path).stdout
        with open(os.path.join(gocache, name + ".json"), "wb") as f:
            f.write(out)
        golden[name] = json.loads(out)
        dets = {}
        for dim in (0, 2600):
            dets[dim] = json.loads(go("goref", "detect", A.ocr_models, path, "0.5", str(dim), str(A.threads)).stdout)
        golden[name]["detections"] = dets
        log(f"Go OCR {name}: {len(golden[name]['page']['lines'])} lines")
    wait_for_memory(".NET OCR")
    e = start_engine(*ocr_args())
    results = {}
    try:
        for name, path in images():
            pixels, meta = raw(path)
            page = e.ocr("recognize", pixels, meta["width"], meta["height"], meta["format"])
            r = {"format": meta["format"], "size": [meta["width"], meta["height"]],
                 "recognize": compare_pages(golden[name]["page"], page), "detect": {}}
            for dim, gdet in golden[name]["detections"].items():
                ndet = e.ocr("detect", pixels, meta["width"], meta["height"], meta["format"], f"?min_score=0.5&max_dimension={dim}")
                r["detect"][str(dim)] = compare_detections(gdet, ndet)
            results[name] = r
            log(f".NET OCR {name}: recognize pass={r['recognize']['pass']} detect pass={all(d['pass'] for d in r['detect'].values())}")
        info = e.json("GET", "/v1/info")
        results["_info_equal"] = info["ocr"] == golden[images()[0][0]]["engine"]
    finally:
        e.close()
    if A.engine_static:
        e = start_engine(*ocr_args(), exe=A.engine_static)
        results["_static"] = {}
        try:
            for name, path in images():
                pixels, meta = raw(path)
                page = e.ocr("recognize", pixels, meta["width"], meta["height"], meta["format"])
                results["_static"][name] = compare_pages(golden[name]["page"], page)
                log(f".NET static-ORT OCR {name}: pass={results['_static'][name]['pass']}")
        finally:
            e.close()
    save("ocr", results)


# ---------------------------------------------------------------- tokenizers

def ocr_lines():
    lines = []
    gocache = os.path.join(A.out, "go-ocr")
    if os.path.isdir(gocache):
        for f in sorted(os.listdir(gocache)):
            with open(os.path.join(gocache, f)) as fh:
                lines += [l["text"] for l in json.load(fh)["page"]["lines"]]
    return lines


TOKENIZERS = {"laya": lambda: os.path.join(A.laya, "tokenizer.json"),
              GEMMA: lambda: os.path.join(A.embedding[GEMMA], "tokenizer.json"),
              HARRIER: lambda: os.path.join(A.embedding[HARRIER], "tokenizer.json")}


def go_tokens(name, texts, special):
    return json.loads(go("goref", "tokenize", TOKENIZERS[name](), "1" if special else "0",
                         stdin=json.dumps(texts, ensure_ascii=False).encode()).stdout)


def net_tokens(name, texts, special, prompt=None):
    args = ["tokenize", TOKENIZERS[name]()] + (["--special"] if special else []) + (["--prompt", prompt] if prompt else [])
    return json.loads(net(*args, stdin=json.dumps(texts, ensure_ascii=False).encode()).stdout)


def step_tokenizers():
    texts = corpus.build(ocr_lines())
    results = {"corpus": len(texts), "ocr_lines": len(ocr_lines()), "max_chars": max(map(len, texts)), "tokenizers": {}}
    for name in TOKENIZERS:
        for special in (False, True):
            g, n = go_tokens(name, texts, special), net_tokens(name, texts, special)
            same = sum(1 for x, y in zip(g, n) if x == y)
            mism = [i for i, (x, y) in enumerate(zip(g, n)) if x != y]
            key = f"{name} special={str(special).lower()}"
            results["tokenizers"][key] = {"identical": same, "total": len(texts), "tokens": sum(map(len, g)),
                                          "first_mismatch": mism[0] if mism else None}
            log(f"tokenizer {key}: {same}/{len(texts)} identical")
    save("tokenizers", results)


# ---------------------------------------------------------------- embeddings

def prompt(model, text, kind):
    if model == HARRIER:
        return "Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery: " + text if kind == "query" else text
    return "task: search result | query: " + text if kind == "query" else "title: none | text: " + text


def valid_text(t):
    return t.strip() != "" and "\0" not in t and len(t.encode()) <= 128 * 1024


def boundary_texts(model, kind):
    """Texts whose prompt has exactly MAX_TOKENS and MAX_TOKENS + 1 tokens."""
    base = "Factura de electricidad pagada por transferencia el 3 de octubre. "
    words = (base * 200).split(" ")
    lo, hi = 1, len(words)
    count = lambda n: len(go_tokens(model, [prompt(model, " ".join(words[:n]), kind)], True)[0])
    while lo < hi:  # smallest n with count(n) >= MAX_TOKENS
        mid = (lo + hi) // 2
        if count(mid) >= MAX_TOKENS:
            hi = mid
        else:
            lo = mid + 1
    out = {}
    for n in range(max(1, lo - 3), lo + 6):
        c = count(n)
        if c in (MAX_TOKENS, MAX_TOKENS + 1) and c not in out:
            out[c] = " ".join(words[:n])
    # Add a character when no word count lands exactly on the limit.
    for c in (MAX_TOKENS, MAX_TOKENS + 1):
        if c not in out:
            for n in range(lo - 3, lo + 6):
                t = " ".join(words[:n])
                for extra in ("x", "x.", "xyz", " 1", " 12"):
                    if count_text(model, t + extra, kind) == c:
                        out[c] = t + extra
                        break
                if c in out:
                    break
    return out


def count_text(model, text, kind):
    return len(go_tokens(model, [prompt(model, text, kind)], True)[0])


def embedding_texts():
    texts = [t for t in corpus.build(ocr_lines()) if valid_text(t)]
    counts = {}
    for model in (GEMMA, HARRIER):
        for kind in ("query", "document"):
            counts[(model, kind)] = [len(x) for x in go_tokens(model, [prompt(model, t, kind) for t in texts], True)]
    ok = [i for i in range(len(texts)) if all(counts[k][i] <= MAX_TOKENS for k in counts)]
    longest = sorted(ok, key=lambda i: -counts[(GEMMA, "document")][i])[:20]
    rest = [i for i in ok if i not in longest]
    chosen = longest + rest[:: max(1, len(rest) // 200)][:200]
    return [texts[i] for i in chosen]


def cosine(a, b):
    dot = sum(x * y for x, y in zip(a, b))
    na = math.sqrt(sum(x * x for x in a))
    nb = math.sqrt(sum(y * y for y in b))
    return dot / (na * nb)


def step_embeddings():
    texts = embedding_texts()
    log(f"{len(texts)} embedding texts")
    results = {"texts": len(texts), "models": {}}
    for model in (GEMMA, HARRIER):
        r = {}
        boundary = {kind: boundary_texts(model, kind) for kind in ("query", "document")}
        # Token counts: the engine's own prompts and tokenizer against Go's.
        for kind in ("query", "document"):
            g = go_tokens(model, [prompt(model, t, kind) for t in texts], True)
            n = net_tokens(model, texts, True, prompt=f"{model}:{kind}")
            r[f"{kind}_token_counts_identical"] = sum(1 for x, y in zip(g, n) if len(x) == len(y))
            r[f"{kind}_token_ids_identical"] = sum(1 for x, y in zip(g, n) if x == y)
        wait_for_memory(f"Go {model}")
        payload = {"queries": texts, "documents": [{"Title": "", "Text": t} for t in texts]}
        gout = json.loads(go("embed", "-model", model, "-models", A.embedding[model], "-threads", str(A.threads),
                             "-max-tokens", str(MAX_TOKENS), stdin=json.dumps(payload, ensure_ascii=False).encode()).stdout)
        gboundary = {}
        for kind, cases in boundary.items():
            for c, t in cases.items():
                p = {"queries": [t], "documents": []} if kind == "query" else {"queries": [], "documents": [{"Title": "", "Text": t}]}
                res = go("embed", "-model", model, "-models", A.embedding[model], "-max-tokens", str(MAX_TOKENS),
                         stdin=json.dumps(p).encode(), check=False)
                gboundary[f"{kind}:{c}"] = "ok" if res.returncode == 0 else ("too_many_tokens" if b"too many tokens" in res.stderr else res.stderr.decode())
        wait_for_memory(f".NET {model}")
        e = start_engine(*embedding_args(model))
        try:
            nvec = {}
            for kind in ("query", "document"):
                vectors = []
                for i in range(0, len(texts), 50):
                    out = e.json("POST", "/v1/embeddings", {"model": model, "kind": kind, "texts": texts[i:i + 50]})
                    assert not out["errors"], out["errors"]
                    vectors += out["vectors"]
                nvec[kind] = vectors
            nboundary = {}
            for kind, cases in boundary.items():
                for c, t in cases.items():
                    out = e.json("POST", "/v1/embeddings", {"model": model, "kind": kind, "texts": [t]})
                    nboundary[f"{kind}:{c}"] = out["errors"][0]["code"] if out["errors"] else "ok"
            info = e.json("GET", "/v1/info")
        finally:
            e.close()
        for kind, gv in (("query", gout["queries"]), ("document", gout["documents"])):
            cos = [cosine(a, b) for a, b in zip(gv, nvec[kind])]
            r[f"{kind}_min_cosine"] = min(cos)
            r[f"{kind}_mean_cosine"] = statistics.fmean(cos)
            r[f"{kind}_max_abs_diff"] = max(abs(x - y) for a, b in zip(gv, nvec[kind]) for x, y in zip(a, b))
            r[f"{kind}_vectors"] = len(cos)
        r["boundary_go"], r["boundary_net"] = gboundary, nboundary
        r["id_equal"] = info["embeddings"][0]["id"] == gout["model"]
        r["id"] = gout["model"]
        r["pass"] = (min(r["query_min_cosine"], r["document_min_cosine"]) >= 0.9999 and gboundary == nboundary
                     and r["query_token_counts_identical"] == len(texts) and r["document_token_counts_identical"] == len(texts)
                     and r["id_equal"])
        results["models"][model] = r
        log(f"embeddings {model}: pass={r['pass']} min cosine {min(r['query_min_cosine'], r['document_min_cosine']):.8f} boundary {nboundary}")
    save("embeddings", results)


# ---------------------------------------------------------------- decisions

def decision_requests():
    rows = []
    with open(os.path.join(REPO, "docs/performance-artifacts/laya/references.json")) as f:
        for g in json.load(f)["groups"]:
            rows += [r["request"] for r in g["rows"]]
    with open(os.path.join(REPO, "internal/decision/testdata/typed-references.json")) as f:
        for g in json.load(f)["groups"]:
            rows += [r["request"] for r in g["rows"]]
    state = "Factura de electricidad. Proveedor: Energía Ciudad. Importe: 42 €. 请付款 🧾"
    rows += [
        {"state": state, "question": "How urgent is this?", "options": ["level 0: none", "level 1: soon", "level 2: now"], "type": "score"},
        {"state": state, "question": "Is this an invoice?", "options": ["false", "true"], "type": "noul"},
        {"state": state, "question": "Which language?", "options": ["English", "Español", "中文", "العربية"]},
        {"state": "  " + state + "\n\n\t", "question": " Category? ", "options": [" a ", "b\n"], "type": "choice"},
        {"state": state * 200, "question": "Too long?", "options": ["no", "yes"]},
        {"state": state, "question": "Long options", "options": ["option " * 60, "short"]},
        {"state": state, "question": "Many options", "options": [f"option number {i} with several words" for i in range(20)]},
        {"state": state, "question": "Too many options", "options": [str(i) for i in range(21)]},
        {"state": state, "question": "One option", "options": ["only"]},
        {"state": state, "question": "Reserved", "options": ["<mask>", "b"]},
        {"state": " ", "question": "Empty state", "options": ["a", "b"]},
        {"state": state, "question": "Blank option", "options": ["a", " "]},
        {"state": state, "question": "Bad type", "options": ["a", "b"], "type": "rank"},
        {"state": state, "question": "noul with three", "options": ["a", "b", "c"], "type": "noul"},
        {"state": state, "question": "q " * 230, "options": ["a", "b"]},
    ]
    return rows


def go_decide(rows):
    """cmd/decide on rows, cached by request: {"results", "model"} or {"error"}."""
    payload = json.dumps({"requests": rows}, ensure_ascii=False).encode()
    cache = os.path.join(A.out, "go-decide", hashlib.sha256(payload).hexdigest() + ".json")
    if os.path.exists(cache):
        with open(cache) as f:
            return json.load(f)
    r = go("decide", "-models", A.laya, "-threads", str(A.threads), stdin=payload, check=False)
    out = json.loads(r.stdout) if r.returncode == 0 else {"error": r.stderr.decode(errors="replace").strip()}
    os.makedirs(os.path.dirname(cache), exist_ok=True)
    with open(cache, "w") as f:
        json.dump(out, f)
    return out


def step_decisions():
    rows = decision_requests()
    payload = json.dumps({"requests": rows}, ensure_ascii=False).encode()
    genc = json.loads(go("goref", "encode", A.laya, stdin=payload).stdout)
    nenc = json.loads(net("laya-encode", A.laya, stdin=payload).stdout)
    identical = sum(1 for a, b in zip(genc, nenc) if a == b)
    errors = sum(1 for a in genc if "error" in a)
    mism = [i for i, (a, b) in enumerate(zip(genc, nenc)) if a != b]
    results = {"encodings": {"requests": len(rows), "identical": identical, "go_errors": errors, "mismatches": mism[:10]}}
    log(f"Laya encodings: {identical}/{len(rows)} identical ({errors} rejected by both)")
    # Inference: valid rows in groups that fit cmd/decide's 64 KiB input,
    # the same groups on both sides so the batches and padding match.
    valid = [r for r, enc in zip(rows, genc) if "error" not in enc]
    sample = valid[::4][:120]
    groups, cur = [], []
    for r in sample:
        if cur and (len(cur) == 16 or len(json.dumps({"requests": cur + [r]}, ensure_ascii=False).encode()) > 60000):
            groups.append(cur)
            cur = []
        cur.append(r)
    if cur:
        groups.append(cur)
    wait_for_memory("Go Laya")
    gres, kept, failing, gid = [], [], [], None
    for gi, grp in enumerate(groups):
        out = go_decide(grp)
        if "error" in out:
            # A row the Go engine rejects at inference fails its whole call:
            # find it, compare it alone, and run the group without it.
            log(f"Go Laya group {gi + 1} failed: {out['error']}; checking its rows one by one")
            for r in grp:
                single = go_decide([r])
                if "error" in single:
                    failing.append((r, single["error"]))
            grp = [r for r in grp if all(r is not f for f, _ in failing)]
            out = go_decide(grp) if grp else {"results": [], "model": gid}
        gres += out["results"]
        gid = out["model"]
        if grp:
            kept.append(grp)
        log(f"Go Laya group {gi + 1}/{len(groups)}")
    groups = kept
    wait_for_memory(".NET Laya")
    e = start_engine(*laya_args())
    nres = []
    try:
        for gi, grp in enumerate(groups):
            out = e.json("POST", "/v1/decisions", {"rows": grp})
            assert not out["errors"], out["errors"]
            nres += out["results"]
        nid = e.json("GET", "/v1/info")["decisions"]["id"]
        # Per-row errors: invalid rows next to valid ones.
        mixed = e.json("POST", "/v1/decisions", {"rows": [rows[-7], valid[0], rows[-11]]})
        failing_net = []
        for r, gerr in failing:
            status, body, _ = e.request("POST", "/v1/decisions", json.dumps({"rows": [r]}, ensure_ascii=False).encode(),
                                        {"Content-Type": "application/json"})
            failing_net.append({"go": gerr, "dotnet": f"{status} {body.decode(errors='replace')}",
                                "same": status == 500 and gerr.split(": ")[-1] in body.decode(errors="replace")})
    finally:
        e.close()
    r = {"rows": len(gres), "groups": len(groups), "index_equal": sum(1 for a, b in zip(gres, nres) if a["index"] == b["index"]),
         "max_logit_diff": max(abs(x - y) for a, b in zip(gres, nres) for x, y in zip(a["logits"], b["logits"])),
         "max_act_logit_diff": max(abs(x - y) for a, b in zip(gres, nres) for x, y in zip(a["act_logits"], b["act_logits"])),
         "max_probability_diff": max(abs(x - y) for a, b in zip(gres, nres) for x, y in zip(a["probabilities"], b["probabilities"])),
         "tokens_equal": all(a["tokens"] == b["tokens"] for a, b in zip(gres, nres)),
         "score_noul_keys_equal": all(("score" in a) == ("score" in b) and ("noul" in a) == ("noul" in b) for a, b in zip(gres, nres)),
         "id_equal": gid == nid, "id": nid,
         "mixed_rows": {"results": [x is not None for x in mixed["results"]], "errors": mixed["errors"]},
         "rows_failing_in_both": failing_net}
    r["pass"] = (r["index_equal"] == r["rows"] and r["max_logit_diff"] <= 1e-4 and r["tokens_equal"] and r["id_equal"]
                 and all(f["same"] for f in failing_net)
                 and results["encodings"]["identical"] == len(rows))
    results["inference"] = r
    log(f"Laya inference: {r}")
    save("decisions", results)


# ---------------------------------------------------------------- bench

def size(path):
    return os.path.getsize(path) / 1024 / 1024


def short_and_long(model):
    short = ["electricity invoice", "¿Dónde está la factura?", "contract renewal date", "Stromrechnung September",
             "bank transfer receipt", "电费发票", "insurance policy", "meeting notes Q3", "payslip October", "hotel booking"]
    long_ = boundary_texts(model, "document")[MAX_TOKENS]
    return short, [long_] * A.runs


def bench_ocr_net(load_image, exe=None):
    e = start_engine(*ocr_args(), idle=10, exe=exe)
    try:
        idle = e.rss()[0]
        pixels, meta = raw(load_image)
        t = time.monotonic()
        e.ocr("recognize", pixels, meta["width"], meta["height"], meta["format"])
        load_s = time.monotonic() - t
        loaded = e.rss()[0]
        e.reset_peak()
        secs = {}
        for i, page in enumerate(A.pages):
            px, m = raw(page)
            for _ in range(A.runs):
                t = time.monotonic()
                e.ocr("recognize", px, m["width"], m["height"], m["format"])
                secs.setdefault(f"page{i}", []).append(time.monotonic() - t)
        peak = e.rss()[1]
        time.sleep(13)
        after = e.rss()[0]
    finally:
        e.close()
    return {"idle_mib": idle, "loaded_mib": loaded, "load_seconds": load_s, "peak_mib": peak, "after_unload_mib": after, "seconds": secs}


def bench_embed_net(model, short, long_, batch):
    e = start_engine(*embedding_args(model), idle=10)
    try:
        idle = e.rss()[0]
        t = time.monotonic()
        e.json("POST", "/v1/embeddings", {"model": model, "kind": "query", "texts": ["hello"]})
        load_s = time.monotonic() - t
        loaded = e.rss()[0]
        e.reset_peak()
        secs = {}
        for key, texts in (("short", short), ("long", long_)):
            for text in texts:
                t = time.monotonic()
                e.json("POST", "/v1/embeddings", {"model": model, "kind": "document", "texts": [text]})
                secs.setdefault(key, []).append(time.monotonic() - t)
        t = time.monotonic()
        e.json("POST", "/v1/embeddings", {"model": model, "kind": "document", "texts": batch})
        secs["batch"] = [time.monotonic() - t]
        peak = e.rss()[1]
        time.sleep(13)
        after = e.rss()[0]
    finally:
        e.close()
    return {"idle_mib": idle, "loaded_mib": loaded, "load_seconds": load_s, "peak_mib": peak, "after_unload_mib": after, "seconds": secs}


def bench_decide_net(rows):
    e = start_engine(*laya_args(), idle=10)
    try:
        idle = e.rss()[0]
        t = time.monotonic()
        e.json("POST", "/v1/decisions", {"rows": rows[:1]})
        load_s = time.monotonic() - t
        loaded = e.rss()[0]
        e.reset_peak()
        secs = {}
        for key, rs in (("row", rows[:1]), ("batch", rows)):
            for _ in range(A.runs):
                t = time.monotonic()
                e.json("POST", "/v1/decisions", {"rows": rs})
                secs.setdefault(key, []).append(time.monotonic() - t)
        peak = e.rss()[1]
        time.sleep(13)
        after = e.rss()[0]
    finally:
        e.close()
    return {"idle_mib": idle, "loaded_mib": loaded, "load_seconds": load_s, "peak_mib": peak, "after_unload_mib": after, "seconds": secs}


def go_profile(spec):
    return json.loads(go("goref", "profile", stdin=json.dumps(spec, ensure_ascii=False).encode()).stdout)


def timed_run(cmd, stdin=None, env=None):
    t = time.monotonic()
    r = subprocess.run(["/usr/bin/time", "-f", "%M", *cmd], input=stdin, capture_output=True, env=env)
    wall = time.monotonic() - t
    if r.returncode != 0:
        raise RuntimeError(r.stderr.decode(errors="replace"))
    return wall, int(r.stderr.decode().strip().splitlines()[-1]) / 1024, r.stdout


def net_cold(args, request, exe=None):
    """Process start to /v1/health, and to the end of the first response."""
    e = Engine(exe or A.engine, socket_path(), [*args])
    try:
        health = e.wait_ready()
        if request:
            request(e)
        first = time.monotonic() - e.started
        hwm = e.rss()[1]
    finally:
        e.close()
    return health, first, hwm


def step_bench():
    A.benchmarking = True
    fixtures = os.path.join(A.out, "fixtures")
    invoice = os.path.join(fixtures, "invoice.png")
    res = {"machine": machine(), "threads": A.threads, "runs": A.runs}
    lib = os.path.join(os.path.dirname(A.engine), "libonnxruntime.so")
    res["size_mib"] = {
        "dotnet_exe": size(A.engine), "dotnet_exe_plus_ort": size(A.engine) + size(lib),
        "go_ocr_cli": size(os.path.join(A.go_bin, "ocr")), "go_embed_cli": size(os.path.join(A.go_bin, "embed")),
        "go_decide_cli": size(os.path.join(A.go_bin, "decide")),
        "go_cli_plus_ort": size(os.path.join(A.go_bin, "decide")) + size(os.path.join(A.native, "lib/libonnxruntime.so")),
        "libonnxruntime_so": size(lib),
    }
    if A.engine_static:
        res["size_mib"]["dotnet_static_ort_exe"] = size(A.engine_static)
    log("sizes", res["size_mib"])
    page = A.pages[0]
    page_px, page_meta = raw(page)
    rows = [r for r in decision_requests()[:400:25]][:16]
    # Cold start: .NET process start to health, and to its first answer.
    cold = {"dotnet_health_no_models": [], "dotnet_health_all_models": []}
    wait_for_quiet("cold start")
    for _ in range(A.runs):
        cold["dotnet_health_no_models"].append(net_cold([], None)[0])
        cold["dotnet_health_all_models"].append(net_cold([*ocr_args(), *embedding_args(GEMMA, HARRIER), *laya_args()], None)[0])
    firsts = {
        "ocr_page0": (ocr_args(), lambda e: e.ocr("recognize", page_px, page_meta["width"], page_meta["height"], page_meta["format"])),
        GEMMA: (embedding_args(GEMMA), lambda e: e.json("POST", "/v1/embeddings", {"model": GEMMA, "kind": "query", "texts": ["hello"]})),
        HARRIER: (embedding_args(HARRIER), lambda e: e.json("POST", "/v1/embeddings", {"model": HARRIER, "kind": "query", "texts": ["hello"]})),
        "laya_row": (laya_args(), lambda e: e.json("POST", "/v1/decisions", {"rows": rows[:1]})),
    }
    for key, (args, request) in firsts.items():
        wait_for_memory(f".NET cold {key}")
        cold[f"dotnet_first_{key}"] = [net_cold(args, request)[1] for _ in range(A.runs)]
    if A.engine_static:
        cold["dotnet_static_health_all_models"] = [net_cold([*ocr_args(), *embedding_args(GEMMA, HARRIER), *laya_args()], None, A.engine_static)[0] for _ in range(A.runs)]
        cold["dotnet_static_first_ocr_page0"] = [net_cold(firsts["ocr_page0"][0], firsts["ocr_page0"][1], A.engine_static)[1] for _ in range(A.runs)]
    env = go_env()
    goc = {}
    for _ in range(A.runs):
        wait_for_memory("Go cold")
        goc.setdefault("go_cli_first_ocr_page0", []).append(
            timed_run([os.path.join(A.go_bin, "ocr"), "-models", A.ocr_models, page], env=env)[0])
        for model in (GEMMA, HARRIER):
            goc.setdefault(f"go_cli_first_{model}", []).append(timed_run(
                [os.path.join(A.go_bin, "embed"), "-model", model, "-models", A.embedding[model]],
                stdin=json.dumps({"queries": ["hello"], "documents": []}).encode(), env=env)[0])
        goc.setdefault("go_cli_first_laya_row", []).append(timed_run(
            [os.path.join(A.go_bin, "decide"), "-models", A.laya], stdin=json.dumps({"requests": rows[:1]}).encode(), env=env)[0])
    cold.update(goc)
    res["cold_seconds"] = {k: median_range(v) for k, v in cold.items()}
    log("cold", {k: round(v["median"], 3) for k, v in res["cold_seconds"].items()})
    # Memory and warm latency: the same workloads in Go (in process, goref)
    # and .NET (over the protocol), one process at a time.
    prof = {}
    wait_for_memory("OCR profile")
    prof["ocr"] = {"go": go_profile({"capability": "ocr", "models": A.ocr_models, "threads": A.threads, "runs": A.runs,
                                     "load_image": invoice, "pages": A.pages})}
    wait_for_memory("OCR profile")
    prof["ocr"]["dotnet"] = bench_ocr_net(invoice)
    if A.engine_static:
        prof["ocr"]["dotnet_static_ort"] = bench_ocr_net(invoice, A.engine_static)
    texts = embedding_texts()
    batch = texts[20:36]
    for model in (GEMMA, HARRIER):
        short, long_ = short_and_long(model)
        wait_for_memory(f"{model} profile")
        prof[model] = {"go": go_profile({"capability": "embed", "model": model, "models": A.embedding[model], "threads": A.threads,
                                         "short": short, "long": long_, "batch": batch})}
        wait_for_memory(f"{model} profile")
        prof[model]["dotnet"] = bench_embed_net(model, short, long_, batch)
    wait_for_memory("Laya profile")
    prof["laya"] = {"go": go_profile({"capability": "decide", "models": A.laya, "threads": A.threads, "runs": A.runs, "rows": rows})}
    wait_for_memory("Laya profile")
    prof["laya"]["dotnet"] = bench_decide_net(rows)
    for cap in prof.values():
        for side in cap.values():
            side["seconds"] = {k: median_range(v) for k, v in side["seconds"].items()}
    res["profile"] = prof
    # The Go CLIs' own numbers: OCR milliseconds per page (the first
    # includes loading), peak RSS of one-page and one-row runs.
    wall, peak, out = timed_run([os.path.join(A.go_bin, "ocr"), "-models", A.ocr_models, *([page] * (A.runs + 1))], env=env)
    ms = [json.loads(o)["milliseconds"] for o in _json_stream(out)]
    res["go_cli_ocr"] = {"wall_seconds": wall, "peak_mib": peak, "milliseconds": ms}
    res["load_average_before_each_measurement"] = A.loads
    save("bench", res)
    log("bench done")


def _json_stream(data):
    dec, text, i, out = json.JSONDecoder(), data.decode(), 0, []
    while i < len(text):
        while i < len(text) and text[i].isspace():
            i += 1
        if i >= len(text):
            break
        obj, i = dec.raw_decode(text, i)
        out.append(json.dumps(obj))
    return out


def machine():
    cpu = ""
    with open("/proc/cpuinfo") as f:
        for line in f:
            if line.startswith("model name"):
                cpu = line.split(":", 1)[1].strip()
                break
    with open("/proc/meminfo") as f:
        mem = int(f.readline().split()[1]) / 1024 / 1024
    return {"cpu": cpu, "cpus": os.cpu_count(), "mem_gb": round(mem, 1), "kernel": os.uname().release}


def main():
    global A
    A = parse_args()
    os.makedirs(A.out, exist_ok=True)
    A.tmp = os.path.join(A.out, "tmp")
    A.benchmarking, A.loads = False, []
    os.makedirs(A.tmp, exist_ok=True)
    for step in A.steps:
        log("step", step)
        globals()["step_" + step]()
    shutil.rmtree(A.tmp, ignore_errors=True)


if __name__ == "__main__":
    main()
