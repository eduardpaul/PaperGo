package dms

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// maxPageCursor bounds the page cursors pageCursor reads.
const maxPageCursor = 4096

// pageCursor is a page position bound to the query and caller that produced
// it. Each listing names its cursors with its own prefix.
type pageCursor[P any] struct {
	Query    string `json:"q"`
	Position P      `json:"at"`
}

// queryFingerprint identifies a normalized query and its caller.
func queryFingerprint(query any) string {
	raw, _ := json.Marshal(query)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func encodePageCursor[P any](prefix, fingerprint string, position P) string {
	raw, _ := json.Marshal(pageCursor[P]{fingerprint, position})
	return prefix + base64.RawURLEncoding.EncodeToString(raw)
}

// decodePageCursor reads a cursor encodePageCursor made with the same prefix
// and fingerprint.
func decodePageCursor[P any](cursor, prefix, fingerprint string) (P, bool) {
	var c pageCursor[P]
	if len(cursor) > maxPageCursor || !strings.HasPrefix(cursor, prefix) {
		return c.Position, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor[len(prefix):])
	if err != nil || json.Unmarshal(raw, &c) != nil || c.Query != fingerprint {
		return c.Position, false
	}
	return c.Position, true
}
