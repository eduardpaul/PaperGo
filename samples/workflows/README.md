# Sample workflows

Workflows that replace the `search.content` built-in for one library, using the [AI steps](../../docs/workflows.md#ai-steps). They need a chat model (`AI_CHAT_*`); with an embedding model (`AI_EMBEDDINGS_*`) their chunks are also found by meaning.

| File | For | Flow |
| --- | --- | --- |
| [`receipts.json`](receipts.json) | receipts (PDFs and photos) | `document.read_text` → `search.chunk` (`whole`) → `ai.complete` (a two-sentence summary) → `search.chunk` (the summary as a `summary` chunk of the same generation) → `search.embed` → `search.publish` |
| [`contracts.json`](contracts.json) | contracts (PDFs) | `document.read_text` → `search.chunk` (`headings`, 2,000 characters) → `search.enrich` (one sentence per section on the contract and clause, with the document as context) → `search.embed` → `search.publish` |

Each is a request body for `POST /v1/workspaces/{id}/workflows`. Replace `$LIBRARY` with the library's ID:

```bash
sed "s/\$LIBRARY/$LIBRARY_ID/g" samples/workflows/receipts.json |
  curl -fsS -X POST "$PAPERGO/v1/workspaces/$WORKSPACE_ID/workflows" \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" --data-binary @-
```

The workflow fills the `search.content` role for that library, so the workspace's `search.content` stops running there. Delete it to return to the workspace's; turning it off turns file content off for the library. A content reindex (`POST /v1/search/reindex` with `{"content": true, "force": true}`) indexes the library's existing files with it. Answers are cached, so files indexed before ask the model nothing again.

`internal/runner/ai_test.go` runs both against a fake chat server.

## Decision actions

These samples use the optional [local decision engine](../../docs/decisions.md),
configured by `AI_DECISION_MODELS_PATH`; they do not need a chat model or a special
classification workflow role.

- [decision-routing.json](decision-routing.json): manual, item-free custom data;
  asks a named choice question and routes with an ordinary `if`.
- [taxonomy-review.json](taxonomy-review.json): reads a document, evaluates a
  managed term set, requests editable approval, then replaces a taxonomy field
  with item-version, current-file and taxonomy guards. No matches and rejection
  finish without writing; a failed guard creates a task asking for recomputation.

Replace `$LIBRARY`, `$TERM_SET` and `$FIELD` in the taxonomy sample. `$FIELD` must
be an existing multi-value term field bound to `$TERM_SET`. Configure the same
set ID in the decision and review picker. Replace `bob` with a readable assignee.
Review keeps the suggested terms prefilled; rejecting needs a comment. For a
single-value field, explicitly pass one approved ID and define empty behavior.

The sample is a normal workflow save request; import it through the existing
workflow API or editor. `internal/runner/decisions_test.go` exercises routing,
review/rejection/no-match paths and refusal after concurrent file, metadata or
taxonomy changes. Probability scores are experimental; representative held-out
production evaluation remains necessary before automatic application.
