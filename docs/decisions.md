# Workflow decisions

`POST /v1/ai/decisions` and `POST /v1/ai/taxonomy-decisions` use one local Laya multilingual FP32 engine. Each is a background operation (`202 Accepted`) whose result is data and probability scores, for API clients and [workflow scripts](workflows.md#waiting) alike. Callers decide how to branch, ask for review or update items. Neither reads images, creates terms, writes metadata or creates tasks.

The shared assets ZIP includes the verified Laya FP32 bundle at `assets/models/laya`. Extract it beside the executable to enable local decisions automatically. The Docker image includes that same layout. For direct Go development commands or an explicit alternative, configure `AI_DECISION_MODELS_PATH` with a verified [Laya bundle](../scripts/decision-model/README.md) and `ONNXRUNTIME_LIB` with the same library used by OCR and embeddings. `AI_DECISION_THREADS` defaults to one, `AI_DECISION_MAX_TOKENS` to 1,024 and `AI_DECISION_HEAD_TOKENS` to 256. Context overflow fails without truncation. Supported limits are 37–1,024 context tokens and 32–256 head tokens, with head + 4 below context. The model is lazy loaded, serialized across callers and unloaded after five idle minutes. One deployment supplies one engine; the workflow form contains no model settings.

## Input sources

Supply exactly one primary source:

- `input`: nonempty text, passed unchanged.
- `data`: a populated JSON object. Nested objects, arrays, false and zero are preserved. Wrap a top-level array or scalar in an object.
- `text_id`: stored text [read from the item's file](documents.md#reading-text), with required `item_id` and `blob_id`. The referenced file must still be current. Optional `context` is a JSON object.

Inline sources name `workspace_id`; arbitrary JSON keys do not imply item access. Inline payloads and context are limited to 16 KiB and eight nesting levels. Document text is kept out of the operation's payload: it holds the stored text's reference. Source ownership is checked when the request is accepted and again when the result commits, including cache hits.

JSON decoding preserves numbers without converting large integers to floating point. JSON object keys sort deterministically, arrays retain order and user text receives no HTML escaping. Pages join with `\n\n`. With context the input is canonical `{"context":{...},"document_text":"..."}`; this uses a separate serializer identity from plain text.

## Generic questions

`ai.decide` takes `questions`, an ordered array of 1–32 questions with at most 256 option entries and 24 KiB of definitions. Each question has a unique lowercase identifier `key`, required `instructions`, optional `type` (default `choice`) and ordered `options` containing `key` and `text`. Option keys use 1–64 letters, digits, underscores or hyphens, without dots. Rendered texts and keys must be unique within a question.

| Type | Options | Result |
| --- | --- | --- |
| `choice` | 2–20 authored texts, unchanged | Highest probability option; a tie uses the first authored option |
| `noul` | Exactly `false`, `true`, in that order | The true probability in `noul`; model text renders `false: <text>` / `true: <text>` |
| `score` | 2–20 ordered levels | Expected zero-based level index in `score`; model text renders `level 0: <text>`, etc. |

A score is ordinal; it does not extract an amount or predict an arbitrary number. Every answer contains `type`, `selected`, `probability` and the full keyed `probabilities`. Only noul answers add `noul`; only score answers add `score`. Probabilities retain their full precision. Raw logits are available from the diagnostic `cmd/decide`, outside normal workflow outputs.

The result's `answers` are keyed by question: `answers.route.selected`, `answers.route.probability`. A low probability is still a result; the caller decides what it means. Import the [workflow samples package](../samples/workflows/README.md) for an item-free custom-data routing workflow.

## Managed taxonomy

`taxonomy.decide` requires an existing organization `term_set_id` and `taxonomy.read`. Workspace membership alone grants no taxonomy access. `selection` defaults to `multiple`; `threshold` defaults to 0.8 and must be greater than 0.5 and at most 1. Both modes return a stable `term_ids` array and a `scores` array for every evaluated candidate, in candidate order.

| Mode | Strategy | Candidates | Evaluation |
| --- | --- | --- | --- |
| `multiple` | `independent-category-v1` | 1–50 | One ordinary choice per term: `[other_text, candidate_text]`; include every term whose second-option probability meets the threshold |
| `single` | `single-category-v1` | 1–19 plus unmatched | One joint choice over candidates followed by unmatched; return the winner only when it is a term above threshold; also return `other_probability` |

The default question is `Which category best describes this document?` and unmatched text is `Other document`. Both can be overridden. Scores in multiple mode are independent and do not sum to one across terms. Options are never shuffled, truncated or silently grouped.

Use either `root_term_id` for a subtree or `term_ids` for an explicit subset. `leaf_only` selects leaves of the eligible active subtree. Deprecated and merged branches and their descendants are ineligible. Explicit missing, wrong-set, inactive or non-leaf selections are refused. Candidate resolution reads complete membership, ordered by `(sort_order, normalized_name, id)`. A snapshot reads at most 10,000 scoped terms; explicit subsets read their ancestors and, for leaf checks, active children. Larger scopes fail closed.

An option's text is its exact `description_overrides` entry (`{term_id,text}`), otherwise the taxonomy description, otherwise the canonical ancestor-name path. Descriptions are not augmented with synonyms, localized labels or UUIDs. Duplicate model texts fail with the relevant term IDs. Empty and oversized sets are refused; a valid result may accept no terms (`term_ids: []`).

Results include `taxonomy_guard` with set ID, normalized scope and a fingerprint of ordered membership and versioned ancestry. The operation reauthorizes taxonomy access and recomputes the fingerprint in its commit transaction. Creation, moves, merges, deprecation, name/label/description edits and eligibility changes invalidate relevant snapshots. Threshold-only changes reuse raw cached probabilities.

## Guarded review and writes

Document results include `source: {item_id,blob_id,text_id,item_version}`. `item_version` is the item's version when the decision read it, and the result commits only while the same file is current. A later write chooses its own precondition: `PATCH /v1/resources/{id}` with `If-Match: "<item_version>"` writes only if the item, its file included, has not changed since.

The [taxonomy-review sample in the importable workflow package](../samples/workflows/README.md) reads a document, evaluates one configured term set, presents an editable term picker in an ordinary approval task, then replaces a multi-value taxonomy field with the approved IDs at the version the decision read. Rejection and no matches finish without a write. A changed item creates a follow-up task asking for recomputation. The package supplies the library, multi-value Topics field, managed terms and owner review tasks. When customizing it, configure the same set ID in both the decision and the task form. A supplied multi-value array replaces the field; it is not appended.

## Cache and execution

Acceptance and commit use short domain transactions. Inference runs outside them on the `decisions` worker. Results use workspace-scoped `ai.decide` / `taxonomy.decide` cache namespaces. Keys include model and artifact identity, serializers, rendering/strategy, source/context identity, ordered questions/options and taxonomy fingerprint. Current source and taxonomy guards stay on the operation and are checked even on cache hits. A conflicting cache writer returns the stored winning payload.

All questions encode and validate before native inference. Each row has at most 48 tokens per option; question and options must fit the head budget intact. Batches have at most eight rows and 2,048 padded row-tokens; one valid long row runs alone. Payloads and results must fit 64 KiB. Workflow run outputs are limited to 16 KiB: return selected IDs and answers instead of complete distributions. Cancelled or failed operations publish no result. Completed inference may remain reusable in the cache after a commit guard fails.

## Validation and quality limits

[Retained measurements](performance-artifacts/laya/README.md) cover 475 synthetic fixture questions and show the strongest results for matched category choices. Go also replays independent official-Python references for ordinal scores, mixed option counts, multilingual JSON and document-plus-context serialization. These establish encoding/numerical correctness, not real-world accuracy or probability calibration. Representative production documents and dedicated held-out score/context evaluations are still needed. Keep human approval in the supplied classification sample.

[Production resource measurements](performance-artifacts/laya/production-resources.json) exercise one/eight/fifty short rows, the maximum 1,024-token row, active cancellation/recovery and simultaneous OCR/embedding/Laya residency. Memory and latency depend on the host, loaded models and input lengths; they are indicative measurements, not a universal bound or SLA.
