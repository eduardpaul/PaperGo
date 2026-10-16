# Laya multilingual versus Julia and Kev

**Laya passes native inference and Go encoding parity across all 475 fixture questions**, using PaperGo's existing ONNX Runtime without graph edits. It materially improves tagging quality over Julia with identical question wording on this fixture. At the existing 0.8 threshold, it trades some precision for more recall than Kev's typed yes/no format. Question format matters: Laya's typed yes/no and neutral A/B protocols perform much worse than its category choice. These are retained experimental measurements. Production actions use the verified Laya bundle; fixture quality does not establish real-document accuracy.

Measured on 15 October 2026 on Linux amd64 (WSL2), Intel Core i7-10870H, four logical CPUs, one inference thread. The unchanged [fixture](../../../scripts/decision-benchmark/fixtures.json) contains 19 synthetic English/Spanish/Catalan documents, eight overlapping tags, 20 positive document/tag pairs and three documents that should receive no tags. [Raw Python results](results.json) retain every probability, threshold result, software version, model/fixture identity and timing. The Julia and Kev reports use the same fixture SHA-256.

## Tag quality

| Model and protocol | Threshold | Precision | Recall | TP / FP / FN | Exact document tag sets |
| --- | ---: | ---: | ---: | ---: | ---: |
| Julia, independent category choice | 0.8 | 22.2% | 80% | 16 / 56 / 4 | 0/19 |
| Kev, same category question/options | 0.8 | 100% | 40% | 8 / 0 / 12 | 8/19 |
| Kev, typed yes/no per tag | 0.8 | 100% | 75% | 15 / 0 / 5 | 14/19 |
| Laya, same category question/options | 0.8 | 85.7% | 90% | 18 / 3 / 2 | 15/19 |
| Laya, typed yes/no per tag | 0.8 | 47.6% | 100% | 20 / 22 / 0 | 6/19 |
| Laya, neutral A/B yes/no choice | 0.8 | — | 0% | 0 / 0 / 20 | 3/19 |

The matched category protocol is byte-identical in question and option text across all three models: `Which category best describes this document?`, with `Other document` and the tag description, independently per tag. The typed protocol repeats Kev's tag-name/definition question and explicit false/true descriptions, but uses each library's own typed serializer. Laya renders `false:` / `true:` labels; Kev renders `no` / `yes` labels. Differences across those typed runs include prompt serialization, not just model weights.

The [model card](https://huggingface.co/convaiinnovations/laya-multilingual) suggests investigating weak yes/no results using a two-option `choice` with neutral keys. That additional fixed protocol uses `A` / `B` with the same negative/positive descriptions. Here it predicts no tags at 0.8, so precision is undefined; the three exact sets are the three negative documents. Its 0.5 diagnostic gives only 4 true positives, 12 false positives and 16 misses. Typed `noul` has the opposite problem on this fixture: many false positives. Neither variant improves the matched category format.

Laya's matched category choice across thresholds:

| Threshold | Precision | Recall | TP / FP / FN | Exact tag sets |
| --- | ---: | ---: | ---: | ---: |
| 0.5 | 50% | 100% | 20 / 20 / 0 | 11/19 |
| 0.6 | 61.3% | 95% | 19 / 12 / 1 | 13/19 |
| 0.7 | 69.2% | 90% | 18 / 8 / 2 | 12/19 |
| 0.8 | 85.7% | 90% | 18 / 3 / 2 | 15/19 |
| 0.9 | 93.8% | 75% | 15 / 1 / 5 | 13/19 |

At 0.8 all three no-tag documents correctly receive no tags under the category protocol. Its errors are specific: the Spanish and Catalan energy bills miss `invoice`; the Catalan payslip incorrectly adds `banking`; the Catalan hotel booking incorrectly adds `electricity` and `employment`. The language breakdown shows where those failures concentrate:

| Language, matched category at 0.8 | Documents | Precision | Recall | Exact tag sets |
| --- | ---: | ---: | ---: | ---: |
| English | 8 | 100% | 100% | 8/8 |
| Spanish | 6 | 100% | 83.3% | 5/6 |
| Catalan | 5 | 62.5% | 83.3% | 2/5 |

Single-choice classification with all eight descriptions plus `Other document` scores **17/19**, compared with Julia's 13/19 and Kev's 18/19. Laya misclassifies the English garden meeting note and negation distractor as `contract`. A selected label counts as correct if it is either expected tag; this baseline cannot recover multiple labels.

All results use the shipped temperatures `[1.0, 1.0, 1.0]`, with no fitted calibration or option-count buckets. The model card explicitly describes the checkpoint as uncalibrated. Thresholds and protocols were fixed before measurement; the 0.5 diagnostic is not a selected production setting. Kev's typed protocol achieves 19/19 exact sets at 0.5 on this same fixture, while Laya's category format has 20 false positives there. Comparing models at one threshold does not establish which model has the best attainable quality. These 19 synthetic documents cannot select a production threshold or establish real-world multilingual accuracy.

## Python CPU and memory

The official `laya==0.4.8` runtime loads the pinned checkpoint into FP32, with compilation, accelerated kernels and CPU autocast disabled. Questions are separate rows, batched eight at a time per tagging request; encoder activations are not shared across question rows. The same tokenizer file is used by Julia and Laya.

| Official Python FP32 workload | Mean per document | p95 | Peak process RSS |
| --- | ---: | ---: | ---: |
| Matched category, eight tags, second pass | 1.82 s | 2.44 s | 2,314 MiB |
| Typed yes/no, eight tags | 2.38 s | 3.04 s | 2,314 MiB |
| Neutral A/B choice, eight tags | 2.29 s | 2.83 s | 2,314 MiB |
| Single nine-option choice | 466 ms | 510 ms | 2,314 MiB |

Model loading takes **9.94 s**. Timings include question construction, encoding, batching and inference, but exclude model loading and the separate public-API parity checks. Peak RSS covers the complete process, including loading transients. The matched corpus runs twice and verifies repeatability within `0.00001`; the other protocols run once with a resident model. All documents and options fit without truncation. The public API's rounded outputs also match the full-precision path on the first document of each protocol.

Kev's Python tagged requests take about 5–7 s with 5,115 MiB peak RSS; Julia's native corpus averages 1.08 s with 528 MiB. These are indicative comparisons: historical Julia load averages were around 4–6, versus 0.4–1.2 for the Laya run, on the same four-CPU host. The execution strategies also differ. The Laya measurements do not establish a controlled speed ratio over Julia.

## Native runtime investigation

The pinned author's exporter runs locally with CPU PyTorch 2.8.0, opset 18 and dynamic batch, sequence and option dimensions. Its five inputs match the application's decision encoder, including the boolean option mask. It retains both decision `logits` and `act_logits` outputs. Exporting takes **34.35 s** and peaks at **5,483 MiB RSS**; the resulting FP32 graph is **1,288,287,578 bytes**, SHA-256 `74dc1d3dccd214c105832b195f191f18c1172b799c78301fdd88f3925608d46e`. [Export metadata](export.json) and [process resources](export-resources.json) are retained. The graph is generated from the verified FP16 checkpoint containers with FP32 computation, without quantization or weight changes.

The [production native replay test](../../../internal/decision/laya_test.go) checks the full [475-question corpus](references.json) across all 76 protocol/document groups. It uses PaperGo's unchanged Go encoder to verify every token, marker and question type before calling the unchanged `internal/onnx` wrapper. It checks selected options, decision logits, softmax probabilities and action logits. The action output has raw logits around ±1,000, so that output uses relative error; tag probabilities use an absolute `0.0001` tolerance. Action confidence is not used to select tags or assessed as a reliable escalation signal.

All selected options and encodings match. Maximum absolute decision-logit difference is `0.000129223`, probability difference `0.000009246`, and relative action-logit difference `0.000004948` (absolute `0.005493165`). [Native measurements](native-results.json) retain every probability. Recomputing [native quality](native-quality.json) gives identical document tag sets at all five thresholds and identical single-choice labels. The [binary/source provenance](native-provenance.json) identifies the replay implementation. Go build, vet, staticcheck, Python syntax checks, artifact checksums and metric recomputation pass.

| Native ONNX Runtime 1.23.2 FP32 workload | Mean inference per document | Peak process RSS |
| --- | ---: | ---: |
| Matched category, eight tags | 2.28 s | 2,020 MiB |
| Typed yes/no, eight tags | 3.09 s | 2,020 MiB |
| Neutral A/B choice, eight tags | 3.07 s | 2,020 MiB |
| Single nine-option choice | 615 ms | 2,020 MiB |

The session loads in **4.72 s**, excluding tokenizer loading. Each group runs once; encoding is checked separately and excluded from the inference timing. [Process resources](native-resources.json) cover the full replay, including tokenizer, model loading and transient allocations. Model files may be in the OS page cache in both Python and native measurements. The native runtime needs the 1.29 GB graph and tokenizer; the FP16 source checkpoint and Python environment are export/evaluation tools. Keeping the source artifacts and generated graph together uses about 1.97 GB, excluding SDKs and the Python environment.

The production engine shares the same ONNX Runtime with OCR and embeddings, validates both output tensors and verifies the supported bundle manifest before loading. The action head remains diagnostic.

The corpus covers batch sizes one and eight, two and nine options, `choice` and `noul` types, and unpadded lengths of 57–183 tokens. This is a short-document evaluation despite the configured 1,024-token limit. The ordinal `score` primitive is not assessed.

## Reproduce and decision for PaperGo

Follow the [model build/install commands](../../../scripts/decision-model/README.md). [artifacts.json](../../../scripts/decision-model/artifacts.json) pins the source checkpoint, official wheel and exporter. The [shared fixture](../../../scripts/decision-benchmark/fixtures.json) is unchanged, and the production test directly replays the retained references.

The category format merits a review-based pilot on real documents, with Catalan hard negatives and overlapping tags represented in a held-out set. Its footprint and quality trade-off are more promising for this app than Julia's measured independent-tag behavior. It still produces confident wrong tags, and a multilingual model label does not remove the need to measure each target language. No long-document, quantized, concurrent-model-residency, ARM64, macOS or Windows evaluation was run. The experimental workflow should retain approval.

## Production integration acceptance

The production engine additionally passes the independent typed/JSON references
in `internal/decision/testdata/typed-references.json`. Those references come from
the official Python serializer, encoder, collator and FP32 eager model, including
mixed option counts with false mask entries.

[Production resource measurements](production-resources.json) use one CPU thread
and bounded batches (eight rows, 2,048 padded row-tokens). They cover 1/8/50 short
rows, a 1,024-token row, active cancellation/recovery, combined resident
PaddleOCR/EmbeddingGemma-2/Laya execution, and idle unload/reload. The short-row
one-row timing includes first load. These are measurements on this development
host, not a memory bound or latency SLA. Human review remains in the supplied
sample; representative real-document/held-out accuracy evaluation is outstanding.
