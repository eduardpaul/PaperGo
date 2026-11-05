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

Immediate completions use any OpenAI-compatible `/v1/chat/completions` endpoint (OpenAI, Ollama, LM Studio, vLLM, llama.cpp): `AI_CHAT_PROVIDER=openai`, `AI_CHAT_MODEL`, `AI_CHAT_ENDPOINT` (default OpenAI's) and, if the server needs one, `AI_CHAT_API_KEY_FILE`. They run at temperature 0 and time out after two minutes. A busy, failing or unreachable provider (429, 5xx or no answer) is called once per attempt: the operation defers itself until the provider's `Retry-After` (at most an hour), else for 30 seconds, without holding a worker, and fails when its attempts run out. Without a chat model, immediate requests are refused, and with no chat model, batch provider or decision engine at all the AI routes are not served. Requests still queued for a model that is gone fail at the next start, with what they need.

Batch completions need their own provider and never fall back to the chat provider. `AI_BATCH_PROVIDER` chooses it, with `AI_BATCH_MODEL`, `AI_BATCH_ENDPOINT` and, when the server needs one, `AI_BATCH_API_KEY_FILE`:

- **`openai`**: the OpenAI Batch protocol, also on Azure OpenAI and Microsoft Foundry bases that end in `/openai/v1`. Requests go up as a JSONL file. An empty `AI_BATCH_MODEL` uses the chat model and an empty `AI_BATCH_ENDPOINT` the chat endpoint, otherwise OpenAI's.
- **`anthropic`**: Anthropic's [Message Batches API](https://platform.claude.com/docs/en/build-with-claude/batch-processing), through the official Go SDK. `AI_BATCH_MODEL` is required, such as `claude-haiku-5-5`; `AI_BATCH_ENDPOINT` defaults to `https://api.anthropic.com`. Requests travel inline with the batch: the prompt as the system prompt, images before the text, and an `output_schema` as `output_config.format`. Structured outputs take no length, range or array-size limits and need every object closed, so PaperGo sends the schema without them and with `additionalProperties: false`, and checks each answer against the whole schema itself. A refused or cut-off answer is a failed line. `AI_BATCH_MAX_TOKENS` bounds each answer (default 16,000, at most 300,000); above 128,000 the batch asks for [extended output](https://platform.claude.com/docs/en/build-with-claude/batch-processing#extended-output-beta) (`output-300k-2026-03-24`), which the Claude API and Claude Platform on AWS offer. Only PaperGo's settings apply: the SDK's `ANTHROPIC_*` environment variables are ignored.

## Batches

Batch completions are `ai.batch` operations, which the `ai-batch` [worker](application-foundation.md#background-operations) admits in groups, one group per queue: by default a full group of 20 distinct requests goes at once, and whatever waits goes at 01:00 UTC. The `workers.ai-batch.admission` section of `config.yaml` changes the threshold, the group size, a maximum wait and the flush schedule; `papergo admin workers flush ai-batch` sends what waits now.

- **Membership.** A group goes to the provider as one batch; identical requests share one line and keep separate authorization. A request whose answer is already cached completes its `ai.batch` operation at acceptance, without waiting for a batch. Repeating its `Idempotency-Key` returns the same operation whether the answer cache is warm or expired.
- **Waiting.** The members of a submitted batch wait without holding a worker (`waiting: remote`). A maintenance task polls each batch every five minutes and ends its members as their answers arrive.
- **Submission.** A durable submission record fixes a batch's membership before upload. Before the batch is created, its members park with the record in one transaction, so a submission that stops at any point after that leaves them with their batch and never claimable for another. Polling looks for an unconfirmed batch by its submission tag in provider metadata. Anthropic batches carry no metadata, so there it is the batch of its size created since the submission that ended holding exactly its request keys, each batch's results read once; while a batch of that size still runs, polling waits. A lookup that cannot establish absence never permits another create, and members return to the queue only after an hour without the batch. A batch the provider refuses gives its members back, with the reason, after five minutes. A line that already succeeded is not sent again.
- **Deadline.** A request still open after `AI_BATCH_DEADLINE` (default `72h`, from `1m` through `720h`) fails.

## Cached answers

Answers are cached by model, endpoint and request: the prompt, schema, input and image. A rerun, reindex or identical file reuses an unexpired answer (`cached: true`). Answers expire after `RESULT_CACHE_TTL` (default `720h`) of idle time; reading one extends it. Concurrent misses can repeat a request. Backups keep cached answers with the database; content packages leave them out.

The cache (`internal/dal/resultcache`, the `cached_results` table) stores a namespace, a sharing scope, a key, a JSON payload of at most 1 MiB and its expiry; each namespace owns its keys. Batch requests and provider responses are kept in durable records independently of the cache, so losing a cached answer never loses an accepted request.

## Samples

[`samples/workflows`](../samples/workflows/README.md) has two `search.content` fillers that call the model immediately: receipts with a model-written summary chunk, and contracts split at headings with model-written context per section. [`samples/receipts-package`](../samples/receipts-package/README.md) extracts structured purchase lines from a receipt photo through batch completions.
