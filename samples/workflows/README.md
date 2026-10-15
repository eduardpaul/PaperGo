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
