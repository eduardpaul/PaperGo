# Documents

A library item holds metadata and immutable file versions. `PUT /v1/items/{id}/content` streams a new file using `X-Filename`, `Content-Type` and the current numeric `If-Match`. The response includes the file version number and the new resource ETag. File version numbers, metadata revision numbers and resource lock versions are independent.

`POST /v1/items/{id}/content/versions/{number}/restore` restores an earlier file as a new file version. It requires item write permission, `list.write` and the current resource `If-Match`. It retains current metadata, restores the selected bytes and filename, and follows the library's publication settings. In a library with explicit publishing, restoring creates a draft and leaves the publication unchanged. Earlier files and metadata revisions remain immutable. File versions share stored bytes within the same item; purging the item queues each object for cleanup once.
