#!/usr/bin/env python3
"""Prints the harness results (OUT/*.json) as Markdown tables for RESULTS.md.

Usage: python3 -I engines/dotnet/bench/report.py OUT
"""

import json
import os
import sys


def load(out, name):
    path = os.path.join(out, name + ".json")
    if not os.path.exists(path):
        return None
    with open(path) as f:
        return json.load(f)


def table(header, rows):
    print("| " + " | ".join(header) + " |")
    print("|" + "|".join("---" for _ in header) + "|")
    for r in rows:
        print("| " + " | ".join(str(c) for c in r) + " |")
    print()


def mr(v, scale=1.0, digits=2):
    if not v:
        return "–"
    return f"{v['median'] * scale:.{digits}f} ({v['min'] * scale:.{digits}f}–{v['max'] * scale:.{digits}f})"


def main():
    out = sys.argv[1]
    ocr = load(out, "ocr")
    if ocr:
        print("### OCR\n")
        rows = []
        for name, r in ocr.items():
            if name.startswith("_"):
                continue
            rec = r["recognize"]
            det = r["detect"]
            rows.append([name, r["format"], f"{r['size'][0]}×{r['size'][1]}", f"{rec['go_lines']} / {rec['net_lines']}",
                         "yes" if rec["texts_identical"] else f"no ({rec.get('char_ratio', 0):.4f})",
                         rec["max_quad_px"], rec["max_word_quad_px"], f"{rec['max_confidence_diff']:.1e}",
                         "yes" if rec["word_counts_equal"] else "no",
                         " / ".join(f"{d['go_regions']}={d['net_regions']}, {d['max_quad_px']}px, {d['max_score_diff']:.0e}" for d in det.values()),
                         "pass" if rec["pass"] and all(d["pass"] for d in det.values()) else "FAIL"])
        table(["image", "format", "size", "lines Go / .NET", "texts identical", "quad Δpx", "word quad Δpx", "confidence Δ",
               "word counts", "detect (default / 2600): regions, Δpx, score Δ", "result"], rows)
        if "_static" in ocr:
            rows = [[n, r["go_lines"], r["max_quad_px"], f"{r['max_confidence_diff']:.1e}", "pass" if r["pass"] else "FAIL"]
                    for n, r in ocr["_static"].items()]
            print("Static ONNX Runtime build (pyke's libonnxruntime.a):\n")
            table(["image", "lines", "quad Δpx", "confidence Δ", "result"], rows)
    tok = load(out, "tokenizers")
    if tok:
        print(f"### Tokenizers\n\nCorpus: {tok['corpus']} strings ({tok['ocr_lines']} OCR lines), longest {tok['max_chars']} characters.\n")
        table(["tokenizer", "identical", "tokens"], [[k, f"{v['identical']}/{v['total']}", v["tokens"]] for k, v in tok["tokenizers"].items()])
    emb = load(out, "embeddings")
    if emb:
        print(f"### Embeddings\n\n{emb['texts']} texts per model and kind.\n")
        rows = []
        for m, r in emb["models"].items():
            for kind in ("query", "document"):
                rows.append([m, kind, r[f"{kind}_vectors"], f"{r[f'{kind}_min_cosine']:.9f}", f"{r[f'{kind}_mean_cosine']:.9f}",
                             f"{r[f'{kind}_max_abs_diff']:.1e}", f"{r[f'{kind}_token_counts_identical']}/{emb['texts']}",
                             ", ".join(f"{k}: {v}" for k, v in r["boundary_net"].items() if k.startswith(kind)),
                             "same" if r["boundary_go"] == r["boundary_net"] else f"DIFF {r['boundary_go']}",
                             "pass" if r["pass"] else "FAIL"])
        table(["model", "kind", "vectors", "min cosine", "mean cosine", "max |Δ|", "token counts", ".NET at 512 / 513 tokens", "Go", "result"], rows)
    dec = load(out, "decisions")
    if dec:
        e, i = dec["encodings"], dec["inference"]
        print("### Decisions\n")
        table(["check", "result"], [
            ["encodings identical (IDs, markers, qtype, or the same error)", f"{e['identical']}/{e['requests']} ({e['go_errors']} rejected by both)"],
            ["rows run / groups", f"{i['rows']} / {i['groups']}"],
            ["same index", f"{i['index_equal']}/{i['rows']}"],
            ["max logit Δ", f"{i['max_logit_diff']:.1e}"],
            ["max act_logit Δ", f"{i['max_act_logit_diff']:.1e}"],
            ["max probability Δ", f"{i['max_probability_diff']:.1e}"],
            ["tokens equal", i["tokens_equal"]],
            ["score/noul fields equal", i["score_noul_keys_equal"]],
            ["ID equal", i["id_equal"]],
            ["result", "pass" if i["pass"] else "FAIL"],
        ])
    b = load(out, "bench")
    if b:
        print("### Benchmarks\n")
        print(f"Machine: {b['machine']}; threads {b['threads']}; {b['runs']} runs; median (min–max).\n")
        table(["binary", "MiB"], [[k, f"{v:.1f}"] for k, v in b["size_mib"].items()])
        table(["cold start", "seconds"], [[k, mr(v, 1, 3)] for k, v in b["cold_seconds"].items()])
        rows = []
        for cap, sides in b["profile"].items():
            for side, p in sides.items():
                rows.append([cap, side, f"{p['idle_mib']:.0f}", f"{p['loaded_mib']:.0f}", f"{p['load_seconds']:.2f}",
                             f"{p['peak_mib']:.0f}", f"{p['after_unload_mib']:.0f}"])
        table(["model", "engine", "idle RSS MiB", "loaded RSS MiB", "first call s", "peak RSS MiB", "after unload MiB"], rows)
        rows = []
        names = [s for s in ("go", "dotnet", "dotnet_static_ort") if any(s in sides for sides in b["profile"].values())]
        for cap, sides in b["profile"].items():
            keys = sorted({k for p in sides.values() for k in p["seconds"]})
            for k in keys:
                rows.append([cap, k] + [mr(sides[s]["seconds"].get(k), 1000, 0) if s in sides else "–" for s in names])
        table(["model", "workload"] + [f"{s} ms" for s in names], rows)
        if "load_average_before_each_measurement" in b:
            loads = [l for _, l in b["load_average_before_each_measurement"]]
            print(f"1-minute load average before the measurements: {min(loads):.2f} to {max(loads):.2f}.\n")
        if "go_cli_ocr" in b:
            c = b["go_cli_ocr"]
            print(f"Go `cmd/ocr` on page0 {len(c['milliseconds'])} times in one process: {c['milliseconds']} ms "
                  f"(the first includes loading), wall {c['wall_seconds']:.2f} s, peak RSS {c['peak_mib']:.0f} MiB.\n")


if __name__ == "__main__":
    main()
