// A bulk request must serialize each operation with the fields of the
// variant its action names: create, update, or one of the lifecycle actions.

import { JsonSerializationWriter } from "@microsoft/kiota-serialization-json";
import { serializeBulkRequest, type BulkRequest } from "../src/client/models/index.js";

declare const process: { exit(code: number): never };

const lifecycle = ["publish", "unpublish", "delete", "archive", "unarchive"] as const;
const request: BulkRequest = {
  operations: [
    { action: "create", parentId: "folder", create: { name: "a" } },
    { action: "update", id: "item", version: 2, update: { name: "b" } },
    ...lifecycle.map((action) => ({ action, id: "item", version: 3 })),
  ],
};
const expected = {
  operations: [
    { action: "create", create: { name: "a" }, parent_id: "folder" },
    { action: "update", id: "item", update: { name: "b" }, version: 2 },
    ...lifecycle.map((action) => ({ action, id: "item", version: 3 })),
  ],
};

const writer = new JsonSerializationWriter();
writer.writeObjectValue(undefined, request, serializeBulkRequest);
const written = JSON.parse(new TextDecoder().decode(writer.getSerializedContent()));
if (JSON.stringify(written) !== JSON.stringify(expected)) {
  console.error("bulk request", JSON.stringify(written));
  process.exit(1);
}
console.log(`a bulk request serializes all ${expected.operations.length} operation actions`);
