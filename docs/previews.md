# File previews

The default **Generate file previews** system workflow fills the `file.preview` role in each workspace. It runs asynchronously on `file.added` and `file.updated`, and supports manual single-item runs. Workspace managers can configure the role through the ordinary built-in workflow controls. It asks for a rendition with `POST /v1/items/{id}/preview`, a background operation that API clients can start too.

A preview operation renders page one of PDFs and TIFFs, or a static JPEG, PNG or WebP image, into a WebP with a longest side of at most 800 pixels and fewer than 100,000 bytes. It starts at quality 75, lowers quality and then dimensions when necessary, and never enlarges an image. Pixel limits apply before image allocation. Invalid, animated, protected or unreadable inputs produce an unavailable state. Rendering needs no OCR. PDF previews use the configured PDFium reader. Previews have their own `previews` worker, so OCR and other long work never delays them.

The operation pins its source while processing. Successful renditions are shared by source SHA-256 and a concurrent success wins over a failed render. The rendition does not replace the original, change item versions or trigger file processing again. A replacement gets its own rendition; retained or published files keep theirs. Failed renditions can be retried by a manual workflow run or another request. The blob store holds rendition bytes and the database pins their metadata. Cleanup removes them after no unreclaimed source file holds those bytes. Workspace/organization packages and administrative backups preserve generated previews and remap their storage objects on import.

`GET /v1/items/{id}/preview` requires `item.read`, `document.read` and access to the item. It resolves the caller’s visible file before reading the shared rendition, so readers cannot inspect an unpublished replacement or another item’s preview. Optional `blob_id` follows the same retained-file permissions as content downloads. Ready responses are `image/webp` with an ETag and `X-Preview-Blob-ID`; pending responses are HTTP 202 with `status: "pending"`, the selected `blob_id` and `Retry-After: 2`. A failed render returns JSON with `status: "unavailable"`. Original file bytes are never sent by this route.

## Page renditions

Viewers that cannot draw a file themselves read its pages as images:

- `GET /v1/items/{id}/pages` lists the pages of the caller's file, or of an earlier version with `blob_id`: `blob_id`, `pages` (each page's `width` and `height` in CSS pixels at 100 %, points at 96 DPI for PDF pages and pixels for images) and `truncated`. PDFs and multi-page TIFFs list every page, up to 2,000; a JPEG, PNG or WebP image has one.
- `GET /v1/items/{id}/pages/{page}?width=` renders one page, from 1, as WebP at the smallest of 320, 480, 640, 960, 1280, 1600 and 2048 pixels wide that is at least `width` (default 960). Tall pages render narrower, to stay within about 8 megapixels, and images are never enlarged.

Pages render on request, a few files at a time, and the server keeps 64 MB of recent renditions in memory, shared by everyone reading the same bytes. It also keeps up to 128 MB of the files it read recently, so scrolling through a scan reads it from storage once; a larger file is read again for each page. With `blob_id` a page is cacheable for a year; without it, it revalidates with its ETag, since the item's file can change. Both routes need `document.read` and read access to the item, and follow the same file visibility as downloads. Other kinds of file answer `415 unsupported_file_type`, and a file whose pages cannot be read answers `422 unreadable_file`.

The web app shows TIFF scans this way, and very large PDFs on small devices.

## In the web app

Gallery and board cards, the item's file bar and File tab, and workflow selection thumbnails use this endpoint. Pending previews show a non-blocking “Generating preview…” placeholder and poll while work is pending. File replacements invalidate the rendition request. A preview is a glance: **Open** shows the whole file in the [file viewer](documents.md#viewing-files), and **Download** retrieves the original bytes.
