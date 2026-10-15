#!/usr/bin/env python3
"""Compare PaperGo's actual Go embedding engines on a fixed labelled smoke set.

Build cmd/embed first. Uses only the Python standard library; Linux/macOS.
Each model runs in an isolated process so peak RSS is measured independently.
"""
import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import resource
import statistics
import subprocess
import sys

HERE = Path(__file__).resolve().parent
MODELS = ["embeddinggemma-2", "harrier-oss-v1-0.6b"]


def quality(queries, documents, fixture, dimensions):
    def normalized(vector):
        vector = vector[:dimensions]
        norm = math.sqrt(sum(v*v for v in vector))
        return [v / norm for v in vector]
    documents = [normalized(v) for v in documents]
    ranks = []
    misses = []
    for q, vector in zip(fixture["queries"], queries):
        vector = normalized(vector)
        scores = [sum(a*b for a,b in zip(vector, doc)) for doc in documents]
        order = sorted(range(len(scores)), key=lambda i: (-scores[i], i))
        rank = min(i+1 for i,j in enumerate(order) if fixture["documents"][j]["id"] in q["relevant"])
        ranks.append((q["language"], rank))
        if rank > 1:
            misses.append({"query": q["text"], "rank": rank, "expected": q["relevant"], "top": fixture["documents"][order[0]]["id"]})
    def metrics(values):
        return {"recall_at_1": sum(r<=1 for r in values)/len(values), "recall_at_5": sum(r<=5 for r in values)/len(values), "mrr": statistics.mean(1/r for r in values)}
    return {**metrics([r for _,r in ranks]), "by_language": {language: metrics([r for lang,r in ranks if lang==language]) for language in sorted({lang for lang,_ in ranks})}, "misses": misses}


def worker(args, fixture):
    model = args.worker
    request = {"queries": [fixture["queries"][0]["text"]]+[q["text"] for q in fixture["queries"]]*args.repeats,
               "documents": [{"title": d["title"], "text": d["text"]} for d in fixture["documents"]]}
    command = [str(args.binary.resolve()), "-model", model, "-models", str((args.models_root/model).resolve()), "-threads", str(args.threads)]
    completed = subprocess.run(command, input=json.dumps(request), text=True, capture_output=True, check=True)
    result = json.loads(completed.stdout)
    timings = sorted(result["query_ms"][1:])
    rss = resource.getrusage(resource.RUSAGE_CHILDREN).ru_maxrss
    if sys.platform == "darwin": rss /= 1024
    files = [args.models_root/model/f for f in ["tokenizer.json", "onnx/model_q4.onnx", "onnx/model_q4.onnx_data"]]
    dims = [768,256] if model==MODELS[0] else [1024]
    return {"model":result["model"], "configure_ms":result["configure_ms"], "cold_query_ms":result["query_ms"][0],
            "warm_query_p50_ms":statistics.median(timings), "warm_query_p95_ms":timings[math.ceil(len(timings)*.95)-1],
            "document_mean_ms":result["document_ms"]/len(fixture["documents"]), "documents_per_second":len(fixture["documents"])/(result["document_ms"]/1000),
            "peak_rss_mib":rss/1024, "artifact_mib":sum(f.stat().st_size for f in files)/1024**2,
            "quality":{str(dim):quality(result["queries"][1:1+len(fixture["queries"])],result["documents"],fixture,dim) for dim in dims}}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, default=Path(".tools/embedding/embed"))
    parser.add_argument("--models-root", type=Path, default=Path(".tools/embedding/models"))
    parser.add_argument("--threads", type=int, default=1)
    parser.add_argument("--repeats", type=int, default=3)
    parser.add_argument("--output", type=Path, default=Path("docs/performance-artifacts/embeddings/results.json"))
    parser.add_argument("--worker", choices=MODELS, help=argparse.SUPPRESS)
    args = parser.parse_args()
    if args.repeats<1: parser.error("repeats must be positive")
    fixture = json.loads((HERE/"retrieval.json").read_text())
    if args.worker:
        print(json.dumps(worker(args,fixture)))
        return
    for name in ["ONNXRUNTIME_LIB"]:
        if not os.environ.get(name): parser.error(f"{name} is required")
    results = []
    for model in MODELS:
        print(f"Benchmarking {model}...",file=sys.stderr,flush=True)
        command = [sys.executable,str(Path(__file__).resolve()),*sys.argv[1:],"--worker",model]
        results.append(json.loads(subprocess.check_output(command,text=True)))
    cpu = next((line.split(":",1)[1].strip() for line in Path("/proc/cpuinfo").read_text().splitlines() if line.startswith("model name")), "unknown") if Path("/proc/cpuinfo").exists() else platform.processor()
    metadata = {"cpu":cpu, "go_version":subprocess.check_output(["go","version"],text=True).strip(), "fixture_sha256":hashlib.sha256((HERE/"retrieval.json").read_bytes()).hexdigest(), "binary_sha256":hashlib.sha256(args.binary.read_bytes()).hexdigest(), "platform":platform.platform(), "machine":platform.machine(), "logical_cpus":os.cpu_count(),
                "threads":args.threads, "repeats":args.repeats, "documents":len(fixture["documents"]), "queries":len(fixture["queries"]),
                "onnxruntime":"1.23.2", "tokenizers":"daulet/tokenizers-v1.27.0", "quantization":"Q4 weights with float32 activations", "fixture":fixture["description"]}
    report = {"environment":metadata,"results":results}
    args.output.parent.mkdir(parents=True,exist_ok=True)
    args.output.write_text(json.dumps(report,indent=2,ensure_ascii=False)+"\n")
    for result in results:
        print(json.dumps({k:v for k,v in result.items() if k!="quality"},indent=2))
        for dim,quality_result in result["quality"].items():
            print(f'{dim}d: R@1={quality_result["recall_at_1"]:.3f}, R@5={quality_result["recall_at_5"]:.3f}, MRR={quality_result["mrr"]:.3f}')


if __name__=="__main__": main()
