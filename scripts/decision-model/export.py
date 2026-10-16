#!/usr/bin/env python3
"""Run the pinned official Laya FP32 exporter with bounded CPU threads."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import resource
import time


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--directory", type=Path, default=Path(".tools/decision-build"))
    args = parser.parse_args()
    root = args.directory.resolve()
    manifest = json.loads(Path(__file__).with_name("artifacts.json").read_text())
    for name, entry in manifest["files"].items():
        if digest(root / name) != entry["sha256"]:
            raise ValueError(f"Checksum mismatch: {name}; run prepare.py")
    os.environ.update(HF_HUB_OFFLINE="1", TRANSFORMERS_OFFLINE="1", USE_TF="0", LAYA_CPU_AMP="",
                      TOKENIZERS_PARALLELISM="false")
    import torch
    torch.set_num_threads(1)
    torch.set_num_interop_threads(1)
    # TorchScript MHA fusion is not an ONNX operator. The exporter uses the
    # upstream dynamic attention implementation and exports ordinary tensor ops.
    torch.backends.mha.set_fastpath_enabled(False)
    spec = importlib.util.spec_from_file_location("official_export", root / "upstream/export_onnx.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    output = root / "onnx"
    output.mkdir(exist_ok=True)
    start = time.perf_counter()
    module.export_to_onnx(str(root / "runtime"), str(output / "model.onnx"))
    tokenizer = output / "tokenizer.json"
    if not tokenizer.exists():
        tokenizer.symlink_to(root / "model/tokenizer/tokenizer.json")
    graph = output / "model.onnx"
    result = {"source_revision": manifest["export_source_revision"], "model_revision": manifest["revision"],
              "torch": torch.__version__, "export_ms": (time.perf_counter()-start)*1000,
              "peak_rss_mib": resource.getrusage(resource.RUSAGE_SELF).ru_maxrss/1024,
              "graph_sha256": digest(graph), "graph_bytes": graph.stat().st_size,
              "export_script_sha256": digest(Path(__file__))}
    (output / "export.json").write_text(json.dumps(result,indent=2)+"\n")
    import shutil
    repo = Path(__file__).resolve().parents[2]
    supported = json.loads((repo / "internal/decision/manifest.json").read_bytes())
    for name in ("model.onnx", "tokenizer.json"):
        if digest(output / name) != supported["files"][name]:
            raise ValueError(f"Export differs from supported bundle: {name}")
    for name in ("manifest.json", "LICENSE", "NOTICE"):
        shutil.copyfile(repo / "internal/decision" / name, output / name)
    print(json.dumps(result),flush=True)


if __name__ == "__main__":
    main()
