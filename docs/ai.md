# AI completions

`POST /v1/ai/completions` asks a chat model to do what `prompt` says with text or an image: summarize, extract, clean up. It is a background operation (`202 Accepted`) for API clients and [workflow scripts](workflows.md#waiting) alike; the operation's result is the answer. Local decisions are separate: see [workflow decisions](decisions.md).

| Field | Meaning |
| --- | --- |
| `prompt` | What to do, 1 to 8,000 characters |
| `input` | Text given directly, such as field values |
| `item_id` | The item whose file the request reads; requests on text given directly name `workspace_id` instead |
| `text_id`, `blob_id` | Text [read from the item's file](documents.md#reading-text) (`blob_id` defaults to its current file) |
| `image_blob_id` | An image file version of the item the model sees, alone or with text |
| `max_chars` | Input sent, 100 to 200,000 (default 20,000) |
| `output_schema` | A [form](workflows.md#forms) of an object: the answer is JSON matching it, else text |
| `execution` | `immediate` or `batch`; empty uses `AI_EXECUTION` (default `immediate`) |
| `queue` | The batch queue (default `shared`) |

The result is `text` or `json`, `cached` and `model`. An answer that does not match `output_schema` fails the operation. A request on an item needs read access to its file versions; a request on text given directly needs read access to the workspace.

## Models

Immediate completions use any OpenAI-compatible `/v1/chat/completions` endpoint (OpenAI, Ollama, LM Studio, vLLM, llama.cpp): `AI_CHAT_PROVIDER=openai`, `AI_CHAT_MODEL`, `AI_CHAT_ENDPOINT` (default OpenAI's) and, if the server needs one, `AI_CHAT_API_KEY_FILE`. They run at temperature 0, time out after two minutes, and are retried on 429 and 5xx, honouring `Retry-After` up to 30 seconds. Without a chat model, immediate requests are refused.

Batch completions need their own provider and never fall back to the chat provider: `AI_BATCH_PROVIDER=openai`, `AI_BATCH_MODEL` (empty uses the chat model), `AI_BATCH_ENDPOINT` (empty uses the chat endpoint, otherwise OpenAI's) and, when the server needs one, `AI_BATCH_API_KEY_FILE`. The client speaks the OpenAI Batch protocol, including Azure bases that end in `/openai/v1`.

## Batches

Batch completions are `ai.batch` operations, which the `ai-batch` [worker](application-foundation.md#background-operations) admits in groups, one group per queue: by default a full group of 20 distinct requests goes at once, and whatever waits goes at 01:00 UTC. The `workers.ai-batch.admission` section of `config.yaml` changes the threshold, the group size, a maximum wait and the flush schedule; `papergo-admin workers flush ai-batch` sends what waits now.

- **Membership.** A group goes to the provider as one batch; identical requests share one line and keep separate authorization. A request whose answer is already cached completes its `ai.batch` operation at acceptance, without waiting for a batch. Repeating its `Idempotency-Key` returns the same operation whether the answer cache is warm or expired.
- **Waiting.** The members of a submitted batch wait without holding a worker (`waiting: remote`). A maintenance task polls each batch every five minutes and ends its members as their answers arrive.
- **Submission.** A durable submission record fixes a batch's membership before upload. After a create that might have reached the provider, reconciliation looks for that record in provider metadata. A lookup that cannot establish absence never permits another create: an unresolved request reaches its deadline instead. A line that already succeeded is not sent again; members of a batch that never reached the provider return to the queue.
- **Deadline.** A request still open after `AI_BATCH_DEADLINE` (default `72h`, from `1m` through `720h`) fails.

## Cached answers

Answers are cached by model, endpoint and request: the prompt, schema, input and image. A rerun, reindex or identical file reuses an unexpired answer (`cached: true`). Answers expire after `RESULT_CACHE_TTL` (default `720h`) of idle time; reading one extends it. Concurrent misses can repeat a request. Backups keep cached answers with the database; content packages leave them out.

The cache (`internal/dal/resultcache`, the `cached_results` table) stores a namespace, a sharing scope, a key, a JSON payload of at most 1 MiB and its expiry; each namespace owns its keys. Batch requests and provider responses are kept in durable records independently of the cache, so losing a cached answer never loses an accepted request.

## Samples

[`samples/workflows`](../samples/workflows/README.md) has two `search.content` fillers that call the model immediately: receipts with a model-written summary chunk, and contracts split at headings with model-written context per section. [`samples/receipts-package`](../samples/receipts-package/README.md) extracts structured purchase lines from a receipt photo through batch completions.
