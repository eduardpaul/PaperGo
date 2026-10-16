# Sample workflows

Import [`workflows.zip`](workflows.zip) from **Administration → Export and import**.
Choose **Try it**, then **Import**. Open **Workflow samples** from the result.
The package creates three empty libraries, four editable workflows, and the
managed terms and field used by taxonomy review. No scripts, ID replacements or
existing libraries are required. Import requires an organization administrator.
To install another independent copy, enter a different workspace name.

| Workflow | Where to use it | Flow |
| --- | --- | --- |
| Index receipts | Upload PDFs or photos to **Receipt summaries** | Read text → whole-document chunk → LLM summary → summary chunk → embed → publish |
| Index contracts | Upload PDFs to **Contracts** | Read text → heading chunks → LLM context per section → embed → publish |
| Decision routing | Run manually from workspace workflows, supplying subject and body | Local named choice → ordinary `if` routing |
| Taxonomy review | Select a file in **Documents to classify** and run manually | Read text → suggest managed topics → owner approval → guarded field update |

The indexing workflows fill `search.content` for their own libraries. They need
a configured chat model (`AI_CHAT_*`); embeddings (`AI_EMBEDDINGS_*`) are optional.
Taxonomy review and routing use the optional [local decision engine](../../docs/decisions.md),
configured by `AI_DECISION_MODELS_PATH`, and do not require a chat model.
Approval tasks go to the imported workspace's owners, initially the importing
administrator. Review keeps suggested terms prefilled; rejecting needs a comment.
A concurrent file, metadata or taxonomy change prevents the update and creates a
recomputation task. Review uses the supplied multi-value **Topics** field.

Open each workflow in the editor to customize it. The importer becomes its
author; runs use that author's permissions. The package contains configuration
only and makes no model calls during import.

For photo extraction into a list of purchased products, import the separate
[receipts package](../receipts-package/README.md), which uses batch execution
and image optimization.

The adjacent JSON files are editable workflow sources used by the regression
suite. `internal/httpapi/samplepackages_test.go` validates the shipped ZIP through
the import API, including dry runs and repeated copies. Maintainers can rebuild
the ZIPs after editing the sources with
`go test ./internal/httpapi -run '^TestSampleWorkspacePackages$' -args -update-sample-packages`
in the normal development environment. Installing samples only requires the ZIP.
