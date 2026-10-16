#!/usr/bin/env python3
"""Install the receipts sample through PaperGo's public HTTP API.

Stdout is one JSON object: workspace_id, library_id, lines_id, workflow_id.
Progress and errors go to stderr. A second run against the same workspace
reuses fields, types, collections and views, and saves the workflow again.
"""
import argparse
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent


class API:
    def __init__(self, base, token):
        self.base = base.rstrip("/")
        self.token = token

    def call(self, method, path, body=None, etag=None):
        data = None if body is None else json.dumps(body).encode()
        request = urllib.request.Request(self.base + path, data=data, method=method)
        request.add_header("Authorization", "Bearer " + self.token)
        if data is not None:
            request.add_header("Content-Type", "application/json")
        if etag:
            request.add_header("If-Match", etag)
        try:
            with urllib.request.urlopen(request) as response:
                raw = response.read()
                payload = json.loads(raw) if raw else None
                return response.status, payload, response.headers.get("ETag")
        except urllib.error.HTTPError as error:
            detail = error.read().decode(errors="replace")
            raise SystemExit(f"{method} {path} failed ({error.code}): {detail}") from None


def rows(payload):
    if isinstance(payload, list):
        return payload
    if isinstance(payload, dict) and isinstance(payload.get("value"), list):
        return payload["value"]
    return []


def find(items, **matches):
    for item in items:
        if all(item.get(key) == value for key, value in matches.items()):
            return item
    return None


def ensure_field(api, spec):
    existing = find(rows(api.call("GET", "/v1/fields")[1]), key=spec["key"])
    if existing:
        return existing["id"]
    body = {
        "key": spec["key"],
        "label": spec["label"],
        "type": spec["type"],
        "indexed": spec.get("indexed", False),
        "scale": spec.get("scale", 0),
        "choices": spec.get("choices", []),
    }
    if spec.get("options"):
        body["options"] = spec["options"]
    _, created, _ = api.call("POST", "/v1/fields", body)
    return created["id"]


def ensure_type(api, spec, field_ids):
    existing = find(rows(api.call("GET", "/v1/content-types")[1]), key=spec["key"])
    if existing:
        return existing["id"]
    _, created, _ = api.call("POST", "/v1/content-types", {
        "key": spec["key"], "name": spec["name"], "field_ids": [field_ids[key] for key in spec["fields"]],
    })
    return created["id"]


def ensure_relationship(api, spec):
    for item in rows(api.call("GET", "/v1/relationship-types")[1]):
        term = item.get("term") or {}
        if term.get("name") == spec["name"]:
            return item.get("id") or term.get("id")
    _, created, _ = api.call("POST", "/v1/relationship-types", spec)
    return created.get("id")


def ensure_workspace(api, spec, workspace_id):
    if workspace_id:
        return workspace_id
    existing = find(rows(api.call("GET", "/v1/workspaces")[1]), name=spec["name"])
    if existing:
        return existing["id"]
    _, created, _ = api.call("POST", "/v1/workspaces", {"name": spec["name"], "description": spec["description"]})
    return created["id"]


def ensure_collection(api, workspace_id, spec, type_id):
    existing = find(rows(api.call("GET", f"/v1/resources/{workspace_id}/children")[1]), name=spec["name"])
    if existing:
        return existing["id"]
    body = {
        "kind": spec["kind"],
        "name": spec["name"],
        "content_type_ids": [type_id],
        "webdav_enabled": spec.get("webdav_enabled", False),
    }
    _, created, _ = api.call("POST", f"/v1/resources/{workspace_id}/children", body)
    return created["id"]


def ensure_view(api, collection_id, spec, type_ids):
    existing = find(rows(api.call("GET", f"/v1/resources/{collection_id}/views")[1]), name=spec["name"])
    if existing:
        return existing["id"]
    query = json.loads(json.dumps(spec["query"]).replace("$receipt_line", type_ids["receipt_line"]).replace("$receipt", type_ids["receipt"]))
    _, created, _ = api.call("POST", f"/v1/resources/{collection_id}/views", {
        "name": spec["name"],
        "layout": spec.get("layout", "table"),
        "is_default": spec.get("is_default", False),
        "columns": spec["columns"],
        "query": query,
    })
    return created["id"]


def ensure_workflow(api, workspace_id, library_id, lines_id, line_type_id):
    raw = (ROOT / "workflow.json").read_text()
    raw = raw.replace("$LIBRARY", library_id).replace("$LINES", lines_id).replace("$LINE_TYPE", line_type_id)
    body = json.loads(raw)
    existing = find(rows(api.call("GET", f"/v1/workspaces/{workspace_id}/workflows")[1]), name=body["name"])
    if not existing:
        _, created, _ = api.call("POST", f"/v1/workspaces/{workspace_id}/workflows", body)
        return created["id"]
    _, current, etag = api.call("GET", f"/v1/workflows/{existing['id']}")
    save = {"name": body["name"], "description": body["description"], "definition": body["definition"], "enabled": True}
    _, updated, _ = api.call("PUT", f"/v1/workflows/{existing['id']}", save, etag or current and f"\"{current['version']}\"")
    return updated["id"]


def main():
    parser = argparse.ArgumentParser(description="Install the receipts sample")
    parser.add_argument("--url", default=os.environ.get("PAPERGO_URL", "http://127.0.0.1:8080"))
    parser.add_argument("--token", default=os.environ.get("PAPERGO_TOKEN"))
    parser.add_argument("--workspace", default=os.environ.get("PAPERGO_WORKSPACE", ""))
    args = parser.parse_args()
    if not args.token:
        raise SystemExit("PAPERGO_TOKEN or --token is required")
    spec = json.loads((ROOT / "resources.json").read_text())
    api = API(args.url, args.token)
    print("creating fields and content types", file=sys.stderr)
    field_ids = {field["key"]: ensure_field(api, field) for field in spec["fields"]}
    type_ids = {item["key"]: ensure_type(api, item, field_ids) for item in spec["content_types"]}
    ensure_relationship(api, spec["relationship"])
    workspace_id = ensure_workspace(api, spec["workspace"], args.workspace)
    print(f"workspace {workspace_id}", file=sys.stderr)
    collections = {}
    for item in spec["collections"]:
        collection_id = ensure_collection(api, workspace_id, item, type_ids[item["content_type"]])
        collections[item["name"]] = collection_id
        for view in item["views"]:
            ensure_view(api, collection_id, view, type_ids)
    library_id = collections["Receipts"]
    lines_id = collections["Receipt lines"]
    workflow_id = ensure_workflow(api, workspace_id, library_id, lines_id, type_ids["receipt_line"])
    print("installed Read receipts", file=sys.stderr)
    json.dump({
        "workspace_id": workspace_id,
        "library_id": library_id,
        "lines_id": lines_id,
        "workflow_id": workflow_id,
    }, sys.stdout)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
