# Dependencies

Direct Go modules and the licences they ship under. Add a row when a change adds a direct dependency, and check its licence first.

| Module | Used for | Licence |
| --- | --- | --- |
| `github.com/stephenafamo/bob` | DAL query building and generated models (`internal/dal` only) | MIT |
| `github.com/stephenafamo/scan` | scanning DAL query rows (`internal/dal` only) | MIT |
| `github.com/aarondl/opt` | optional and nullable columns of generated models | MIT |
| `github.com/jackc/pgx/v5` | PostgreSQL driver | MIT |
| `github.com/blevesearch/snowballstem` | word stems for search in the `SEARCH_LANGUAGES` (`internal/search` only): the Snowball project's generated stemmers | BSD-3-Clause |
| `github.com/coreos/go-oidc/v3` | OIDC bearer token verification | Apache-2.0 |
| `github.com/yalue/onnxruntime_go` | cgo ONNX Runtime binding shared by OCR, embeddings and decisions | MIT |
| `github.com/klippa-app/go-pdfium` | PDF text reading and rendering with its embedded PDFium WebAssembly module (`internal/doctext/pdfium` only) | MIT |
| `github.com/tetratelabs/wazero` | pure-Go WebAssembly runtime that runs PDFium in-process (`internal/doctext/pdfium` only) | Apache-2.0 |
| `github.com/daulet/tokenizers` | statically linked Hugging Face Rust tokenizer | MIT |
| `github.com/go-jose/go-jose/v4` | JWT handling | Apache-2.0 |
| `github.com/google/uuid` | identifiers | BSD-3-Clause |
| `golang.org/x/crypto` | password hashing (`bcrypt`) | BSD-3-Clause |
| `golang.org/x/image` | WebP and TIFF decoding of uploaded images, image resampling for OCR, fonts in OCR tests | BSD-3-Clause |
| `golang.org/x/net` | international host names in their ASCII form for [trusted hosts](trusted-hosts.md) (`internal/hosts`, `idna`) | BSD-3-Clause |
| `golang.org/x/oauth2` | bearer-token transport for OIDC UserInfo | BSD-3-Clause |
| `golang.org/x/sys` | database file leases and atomic backup publishing (`unix`, `windows`) | BSD-3-Clause |
| `golang.org/x/term` | the interactive first-start password prompt | BSD-3-Clause |
| `golang.org/x/text` | diacritic folding in the PostgreSQL search store and language preferences | BSD-3-Clause |
| `github.com/klauspost/compress` | zstd compression of stored run output (`internal/dal/runs`) | BSD-3-Clause; zstd's bundled xxhash is MIT |
| `gopkg.in/yaml.v3` | reading `config.yaml` | MIT and Apache-2.0 |
| `github.com/emersion/go-ical` | RFC 5545 decoding and encoding for HTTPS iCalendar integrations | MIT |
| `github.com/teambition/rrule-go` | bounded recurrence expansion for HTTPS iCalendar integrations | MIT |
| `github.com/robfig/cron/v3` | parsing workflow schedule triggers | MIT |
| `modernc.org/sqlite` | SQLite driver | BSD-3-Clause |
| `github.com/dop251/goja` | JavaScript runtime for workflow scripts (`internal/script` only) | MIT |
| `github.com/evanw/esbuild` | bundling workflow scripts (`internal/script` only) | MIT |
| `github.com/modelcontextprotocol/go-sdk` | the [MCP endpoint](mcp.md)'s protocol and Streamable HTTP transport (`extensions/mcp` only) | Apache-2.0, older contributions MIT |
| `github.com/google/jsonschema-go` | validating MCP tool arguments against their schemas (`extensions/mcp` only) | MIT |

goja also links `github.com/dlclark/regexp2` (MIT), `github.com/go-sourcemap/sourcemap` (BSD-2-Clause) and `github.com/google/pprof` (Apache-2.0).

go-pdfium's WebAssembly backend also links `github.com/jolestar/go-commons-pool/v2` (Apache-2.0).

**PDFium** 8111, as the WebAssembly module go-pdfium v1.21.4 embeds, is compiled into the executable and runs in wazero v1.12.0 with no file system access; no native PDFium library is loaded. The module is built with Emscripten 6.0.13 from the `wasm-standalone` branch of `jerbob92/pdfium-binaries`, without V8 or XFA. PDFium is Apache-2.0 and BSD-3-Clause. The module also contains FreeType (FreeType License), libjpeg-turbo (IJG, BSD-3-Clause and zlib), OpenJPEG (BSD-2-Clause), Little CMS (MIT), zlib, ICU (Unicode License), Abseil (Apache-2.0), AGG and Emscripten's runtime libraries (MIT and NCSA, musl MIT). `internal/doctext/pdfium/notices` holds these notices and the rest of PDFium 8111's licence set as `bblanchon/pdfium-binaries` chromium/8111 ships it (among them dragonbox, HarfBuzz and Highway), plus wazero's licence and NOTICE. `scripts/build-binaries.py` ships them as `notices/pdfium/` in release archives and the Docker image. Update the notices and `Name()` in `internal/doctext/pdfium` when go-pdfium changes.

The MCP SDK also links `github.com/segmentio/encoding` and `github.com/segmentio/asm` (MIT), `github.com/yosida95/uritemplate/v3` (BSD-3-Clause) and `golang.org/x/time` (BSD-3-Clause).

The embedded workflow SDK (`sdk/dist/sdk.js`) bundles the npm packages `@microsoft/kiota-abstractions` and the Kiota JSON, text, form and multipart serializers (MIT), `@opentelemetry/api` and `@std-uritemplate/std-uritemplate` (Apache-2.0), and `tinyduration` (MIT). The client is generated with Kiota 1.35.0 (MIT), a development tool.


The native SDK installer (`scripts/native.py`, checksums in `scripts/native.json`) provisions these libraries:

- **ONNX Runtime** 1.23.2 (MIT). Its release binaries include third-party code listed in their `ThirdPartyNotices.txt`, among it Eigen, partly under MPL-2.0. Deployments that ship the library ship that file with it.
- **The PaddleOCR models** in `models/paddleocr` (Apache-2.0, PaddlePaddle / PaddleOCR contributors).
- **Hugging Face Tokenizers** through the pinned `daulet/tokenizers` v1.27.0 Rust static library, linked into the executable. The library is daulet's FFI crate (MIT) around Hugging Face `tokenizers` 0.22.0 (Apache-2.0), with 66 further Rust crates under MIT, Apache-2.0, Unlicense, Zlib, BSL-1.0 or BSD-2-Clause terms (most dual-licensed MIT OR Apache-2.0), the Oniguruma C library (BSD-2-Clause) that `onig_sys` compiles, the esaxx and sais C++ code (MIT) that `esaxx-rs` compiles, OpenAI's tiktoken vocabularies (MIT) embedded by `tiktoken-rs`, and the Rust standard library (MIT OR Apache-2.0). `scripts/generate-tokenizers-notices.py` lists the crates linked into the release library from the pinned source's `Cargo.lock` and writes their licence texts to `scripts/native-notices/tokenizers/`; `scripts/native.py` ships that directory as `notices/tokenizers/` in release archives and the Docker image. Regenerate it whenever `tokenizers_source` changes.
- **Microsoft Visual C++ runtime** 14.44.35211 for Windows x64 (`vcruntime140.dll`, `vcruntime140_1.dll`, `msvcp140.dll`, `msvcp140_1.dll`), which ONNX Runtime's Windows build imports. `native.json` pins Microsoft's Visual Studio package `Microsoft.VC.14.44.17.14.CRT.Redist.X64.base`, whose `VC\Redist\MSVC\14.44.35112\x64\Microsoft.VC143.CRT` folder holds the files Visual Studio's licence lets applications redistribute app-locally (Distributable Code). ONNX Runtime 1.23.2 is built with the 14.40 toolset and needs this runtime at 14.40 or newer. `papergo.exe` embeds the files unmodified; `scripts/native.py` ships `scripts/native-notices/microsoft-visual-cpp-runtime.txt` as `notices/microsoft-visual-cpp-runtime.txt` in the Windows ZIP. That notice names the files, version, source package, the redistribution list and the Visual Studio licence terms, states that the files are not licensed under PaperGo's Apache License 2.0 but only under Microsoft's terms, which it restates, and that installing, copying or using the Windows build means accepting those terms and redistributing it means requiring recipients to accept them, as the Distributable Code terms require. `scripts/release-package.py` refuses a Windows ZIP whose notice lacks the pinned version, the statement that the files are not under PaperGo's Apache License or the acceptance clause. Update its version with the pin.

PaperGo itself is Apache-2.0 (`LICENSE`, `NOTICE`); every release ZIP carries both at the top and the Docker image under `/opt/papergo`. Linux release ZIPs carry ONNX Runtime and notices beside the executable, in `lib/` and `notices/`. The executable and ONNX Runtime use the distribution's libstdc++ and libgcc_s, which no release ships: the executable links them dynamically, as `-lstdc++` from tokenizers asks, because ONNX Runtime loads the system libstdc++ anyway and one C++ runtime per process is simplest. The release needs only old symbol versions from them, which `scripts/release-package.py` caps at what Ubuntu 24.04 and Debian 13 provide ([Releases](releases.md)). The Windows ZIP carries only notices: `papergo.exe` embeds ONNX Runtime and the Visual C++ runtime ([Windows runtime](releases.md#windows-runtime)), and links MinGW's libstdc++, libgcc and winpthreads statically. Their licences ship from MSYS2's `share/licenses` as `notices/compiler/libstdc++/` and `notices/compiler/libgcc/` (GPL-3.0 with the GCC Runtime Library Exception, `COPYING3` and `COPYING.RUNTIME`) and `notices/compiler/winpthreads/COPYING` (MIT and BSD-3-Clause). The Docker image uses the Linux layout under `/opt/papergo`, on `distroless/cc-debian13`, which provides Debian 13's libstdc++6 and libgcc-s1. Models and their notices ship in the platform-independent assets ZIP.

Searchable PDFs embed `internal/pdf/text.ttf`, a placeholder font generated by `scripts/generate-pdf-text-font.py` (PaperDotNet's `OcrText.ttf`, byte for byte). FontTools builds it and is not a dependency.

Web numeric transport, long tables and component tests use these direct packages. The complete frontend dependency list is in [`web/package.json`](../web/package.json).

| Package | Used for | Licence |
| --- | --- | --- |
| `lossless-json` | exact JSON numeric fields in API responses, mutations and upload metadata | MIT |
| `oidc-client-ts` | web sign-in at the OIDC provider: authorization code with PKCE, refresh and sign-out (brings `jwt-decode`, MIT) | Apache-2.0 |
| `@tanstack/react-virtual` | rendering only the visible rows of long list tables | MIT |
| `pdfjs-dist` | the PDF file handler: rendering, text layer and find, loaded only when a PDF opens; its legacy build carries the polyfills older browsers need, and its CMaps, standard fonts, ICC profiles and image decoders ship as versioned assets | Apache-2.0 |
| `docx-preview` | the Word file handler: laying out `.docx` pages in the browser, loaded only when a Word file opens | Apache-2.0 |
| `jszip` | reading the `.docx` archive for `docx-preview` | MIT (dual-licensed MIT or GPL-3.0; used under MIT) |
| `react-markdown`, `remark-gfm` | the Markdown file handler: rendering GitHub-flavoured Markdown (tables, task lists, strikethrough) to React elements without raw HTML, loaded only when a Markdown file opens | MIT |
| `@testing-library/react` | tests of rendered forms and user interactions | MIT |
| `jsdom` | DOM environment for component tests | MIT |

MIT, Apache-2.0, BSD (BSD-2-Clause, BSD-3-Clause and 0BSD), ISC, zlib, BSL-1.0 and PostgreSQL licenses are allowed. The GCC Runtime Library Exception permits linking the compiler runtimes into the Windows executable without applying GPL terms to PaperGo. Redistributing the Visual C++ runtime requires a Visual Studio licence, under its Distributable Code terms. ONNX Runtime's Eigen dependency includes MPL-2.0 files; its upstream notices and source reference ship with the library.

The bundled local decision engine uses Laya multilingual FP32, checkpoint
`1720e3e3357cfe1e281542e223f8273b0890ca34` (Apache-2.0). Its verified bundle includes
model/tokenizer hashes, graph provenance, LICENSE and NOTICE. The Go marker
serialization retains upstream attribution in `internal/decision/NOTICE`.
See [model build/install](../scripts/decision-model/README.md). Python and PyTorch
are model-build dependencies; they are not application runtime dependencies.

Standalone API builds also include the pinned EmbeddingGemma 2 (Google DeepMind, Apache-2.0) and Harrier-OSS-v1-0.6B (Microsoft, MIT) text-only Q4 exports. Their model cards ship beside the extracted weights; license texts and attribution ship under `notices/models`. Revisions and file hashes are in `scripts/fetch-embedding-models.py`.

## Third-party notices

Release builds generate the notices of the Go modules and npm packages compiled into the executable; every release ZIP and the Docker image carry them, and `scripts/release-package.py` refuses a ZIP without them:

- `notices/go/THIRD-PARTY-NOTICES`: `scripts/generate-go-notices.py` lists every module `go list -deps ./cmd/api` reports, but PaperGo's own, with the environment `scripts/build-binaries.py` builds with (GOOS, GOARCH, cgo), so each platform's ZIP lists what its executable links. It writes each module's path, version and every LICENSE, COPYING, NOTICE and PATENTS file at its module root (a replaced module's from its replacement), and the Go standard library's licence from GOROOT. Inspect the current platform's with `python3 scripts/generate-go-notices.py` after sourcing the native environment; it needs `web/dist` built, which the executable embeds.
- `notices/web/THIRD-PARTY-NOTICES` and `notices/sdk/THIRD-PARTY-NOTICES`: `scripts/npm-notices.mjs` lists every production entry of the project's `package-lock.json` (none marked `dev`), including packages the bundle tree-shakes away, with name, version, `license` field and the text of each licence, copying and notice file at the package root, or a statement that the package ships none. It reads the installed `node_modules`, so it runs in the builds that have them: `npm --prefix web run build` writes `web/dist/THIRD-PARTY-NOTICES` (embedded and served with the web UI), in the release workflow, `scripts/check.py` and the Dockerfile's frontend stage alike. `npm --prefix sdk run build` writes `sdk/dist/THIRD-PARTY-NOTICES`, committed beside the committed bundle; the lint check's generated-code step rebuilds the SDK and fails when it changed. Optional platform packages npm did not install, such as other platforms' `@napi-rs/canvas` binaries, cannot be in a bundle and are left out.

Both generators fail on a module or package without a licence file they can read (Go) and on a licence outside the allowed list above. Go licences are recognised from their texts; npm licences from the SPDX `license` expression, where one allowed choice of an `OR` suffices. Reviewed exceptions are listed in the generators and pinned: a Go module by the SHA-256 of its licence files, an npm package by its exact `license` field, so any change needs a new review:

- `github.com/modelcontextprotocol/go-sdk`: code under Apache-2.0, older contributions MIT; the same LICENSE puts the project's documentation, which is not linked, under CC-BY-4.0.
- `modernc.org/sqlite`: BSD-3-Clause, with SQLite's public-domain dedication and sqlite-vec's MIT licence. Its `LICENSE-3RD-PARTY.md` quotes the MPL-2.0 of `github.com/hashicorp/golang-lru`, which it lists as compiled into nothing; `go list -deps` confirms PaperGo does not link it.
- `isbot` (web, through `@tanstack/react-router`): Unlicense, a public-domain dedication. It detects crawlers during server rendering, which the embedded web UI does not use, and the build drops its code.

A Go module without a licence file needs its upstream licence pinned under `scripts/native-notices/go/` and handled in `scripts/generate-go-notices.py`, as the tokenizers notices handle a crate without one.
