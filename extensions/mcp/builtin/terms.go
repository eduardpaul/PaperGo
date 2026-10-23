package builtin

import (
	"context"
	"encoding/json"
	"net/url"

	"papergo/extensions/mcp"
)

// keywordsKey names the intrinsic keywords in list_terms, as in queries.
const keywordsKey = "$keywords"

// maxTermSetPages bounds the search for the keywords set.
const maxTermSetPages = 10

func listTerms() mcp.Tool {
	const name = "list_terms"
	return mcp.Tool{
		Name:  name,
		Title: "List terms",
		Description: "Lists the terms a term or keywords field can hold, or the organization's keywords, so values and filters use existing terms " +
			"instead of guessed labels. Name the field with collection_id and field_key ($keywords for the keywords every entry has), or give a term_set_id. " +
			"Without search or parent_id it lists the top-level terms; parent_id lists a term's children (has_children tells which have any), " +
			"and search matches names, labels and synonyms anywhere below parent_id.",
		InputSchema: object(nil,
			property("collection_id", idProperty("The list or library whose field_key to list terms for.")),
			property("field_key", `{"type": "string", "minLength": 1, "maxLength": 64, "description": "A term or keywords field of the collection, or $keywords."}`),
			property("term_set_id", idProperty("A term set, instead of collection_id and field_key.")),
			property("search", `{"type": "string", "minLength": 1, "maxLength": 255, "description": "Words in the terms' names, labels or synonyms."}`),
			property("parent_id", idProperty("A term whose children to list.")),
			property("limit", limitProperty(maxLimit, defaultLimit)), property("cursor", cursorProperty)),
		OutputSchema: pageSchema(`{"type": "object", "required": ["id", "name", "parent_id", "has_children"], "properties": {
			"id": {"type": "string"}, "name": {"type": "string"}, "description": {"type": "string"}, "parent_id": `+nullableString+`,
			"has_children": {"type": "boolean"}, "synonyms": {"type": "array", "items": {"type": "string"}},
			"labels": {"type": "object", "additionalProperties": {"type": "string"}, "description": "Names by language."}}}`,
			property("term_set_id", `{"type": "string"}`)),
		Scopes:      []string{scopeTaxonomyRead},
		Annotations: mcp.Annotations{ReadOnly: true, Idempotent: true},
		Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (json.RawMessage, error) {
			var in struct {
				CollectionID string `json:"collection_id"`
				FieldKey     string `json:"field_key"`
				TermSetID    string `json:"term_set_id"`
				Search       string `json:"search"`
				ParentID     string `json:"parent_id"`
				Limit        *int   `json:"limit"`
				Cursor       string `json:"cursor"`
			}
			if err := decode(args, &in); err != nil {
				return nil, err
			}
			bySet, byField := in.TermSetID != "", in.CollectionID != "" || in.FieldKey != ""
			if bySet == byField || byField && (in.CollectionID == "" || in.FieldKey == "") {
				return nil, mcp.Fail(mcp.CodeInvalidArguments, "give collection_id with field_key, or term_set_id")
			}
			p, err := newPaging(name, c, args)
			if err != nil {
				return nil, err
			}
			token, err := p.resume(in.Cursor)
			if err != nil {
				return nil, err
			}
			set := in.TermSetID
			if set == "" {
				if set, err = fieldTermSet(ctx, c, in.CollectionID, in.FieldKey); err != nil {
					return nil, err
				}
			}
			path := "/v1/term-sets/" + url.PathEscape(set) + "/terms"
			query := url.Values{"$top": {limit(in.Limit, defaultLimit)}}
			for k, v := range map[string]string{"q": in.Search, "parent_id": in.ParentID, "$skiptoken": token} {
				if v != "" {
					query.Set(k, v)
				}
			}
			var pg struct {
				Value    []term  `json:"value"`
				NextLink *string `json:"@odata.nextLink"`
			}
			if err := get(ctx, c, path, query, &pg); err != nil {
				return nil, err
			}
			next, err := p.next(pg.NextLink, path, query)
			if err != nil {
				return nil, err
			}
			if pg.Value == nil {
				pg.Value = []term{}
			}
			return encode(struct {
				list[term]
				TermSetID string `json:"term_set_id"`
			}{list[term]{Value: pg.Value, NextCursor: next}, set})
		},
	}
}

// term is a term as list_terms returns it.
type term struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	ParentID    *string           `json:"parent_id"`
	HasChildren bool              `json:"has_children"`
	Synonyms    []string          `json:"synonyms,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// fieldTermSet is the term set a field of a collection takes terms from.
func fieldTermSet(ctx context.Context, c mcp.Caller, collectionID, key string) (string, error) {
	if key == keywordsKey {
		return keywordsSet(ctx, c)
	}
	fs, err := fields(ctx, c, collectionID)
	if err != nil {
		return "", err
	}
	for _, f := range fs {
		switch {
		case f.Key != key:
		case f.Type == fieldTerm && f.Options.TermSetID != "":
			return f.Options.TermSetID, nil
		case f.Type == fieldKeywords:
			return keywordsSet(ctx, c)
		default:
			return "", mcp.Fail(mcp.CodeInvalidArguments, key+" is a "+f.Type+" field; give a term or keywords field, or $keywords")
		}
	}
	return "", mcp.Fail(mcp.CodeInvalidArguments, "the collection has no field "+key+"; get_collection_schema lists its fields")
}

// keywordsSet is the organization's keywords set.
func keywordsSet(ctx context.Context, c mcp.Caller) (string, error) {
	query := url.Values{"$top": {"200"}}
	for range maxTermSetPages {
		var pg struct {
			Value []struct {
				ID         string `json:"id"`
				IsKeywords bool   `json:"is_keywords"`
			} `json:"value"`
			NextLink *string `json:"@odata.nextLink"`
		}
		if err := get(ctx, c, "/v1/term-sets", query, &pg); err != nil {
			return "", err
		}
		for _, s := range pg.Value {
			if s.IsKeywords {
				return s.ID, nil
			}
		}
		if pg.NextLink == nil {
			break
		}
		u, err := url.Parse(*pg.NextLink)
		if err != nil || u.Query().Get("$skiptoken") == "" {
			break
		}
		query.Set("$skiptoken", u.Query().Get("$skiptoken"))
	}
	return "", mcp.Fail(mcp.CodeNotFound, "PaperGo has no keywords set")
}
