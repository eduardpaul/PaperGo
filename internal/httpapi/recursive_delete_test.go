package httpapi

import (
	"papergo/internal/dms"
	"testing"
)

func TestFolderDeleteRequiresRecursiveIntentUnlessEmpty(t *testing.T) {
	for _, kind := range []string{"list", "library"} {
		t.Run(kind, func(t *testing.T) {
			h, s, _ := setup(t)
			ctx := t.Context()
			w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Demo"})
			if err != nil {
				t.Fatal(err)
			}
			collection, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: kind, Name: "Records"})
			if err != nil {
				t.Fatal(err)
			}
			folder, err := s.Create(ctx, "alice", collection.ID, dms.CreateResource{Kind: "folder", Name: "Parent"})
			if err != nil {
				t.Fatal(err)
			}
			nested, err := s.Create(ctx, "alice", folder.ID, dms.CreateResource{Kind: "folder", Name: "Nested"})
			if err != nil {
				t.Fatal(err)
			}
			item, err := s.Create(ctx, "alice", nested.ID, dms.CreateResource{Kind: "item", Name: "Child.txt"})
			if err != nil {
				t.Fatal(err)
			}
			outside, err := s.Create(ctx, "alice", collection.ID, dms.CreateResource{Kind: "item", Name: "Outside.txt"})
			if err != nil {
				t.Fatal(err)
			}
			path := "/v1/resources/" + folder.ID
			for _, query := range []string{"", "?recursive=false"} {
				response := request(h, "DELETE", path+query, "", testToken, `"1"`, "")
				if response.Code != 409 {
					t.Fatal("ordinary delete accepted populated folder", response.Code, response.Body.String())
				}
			}
			for _, query := range []string{"?recursive=", "?recursive=yes", "?recursive=1", "?recursive=true&recursive=false"} {
				response := request(h, "DELETE", path+query, "", testToken, `"1"`, "")
				if response.Code != 400 {
					t.Fatal("invalid recursive intent accepted", query, response.Code, response.Body.String())
				}
			}
			for _, precondition := range []struct {
				match string
				code  int
			}{{"", 428}, {`"99"`, 412}} {
				response := request(h, "DELETE", path+"?recursive=true", "", testToken, precondition.match, "")
				if response.Code != precondition.code {
					t.Fatal("recursive precondition ignored", response.Code, response.Body.String())
				}
			}
			for _, id := range []string{folder.ID, nested.ID, item.ID, outside.ID} {
				response := request(h, "GET", "/v1/resources/"+id, "", testToken, "", "")
				if response.Code != 200 || response.Header().Get("ETag") != `"1"` {
					t.Fatal("rejected request changed content", response.Code, response.Body.String())
				}
			}
			response := request(h, "DELETE", path+"?recursive=true", "", testToken, `"1"`, "")
			if response.Code != 204 {
				t.Fatal("recursive delete failed", response.Code, response.Body.String())
			}
			for _, id := range []string{folder.ID, nested.ID, item.ID} {
				if response = request(h, "GET", "/v1/resources/"+id, "", testToken, "", ""); response.Code != 404 {
					t.Fatal("recursive deletion left visible content", response.Code, response.Body.String())
				}
			}
			if response = request(h, "GET", "/v1/resources/"+outside.ID, "", testToken, "", ""); response.Code != 200 {
				t.Fatal("recursive deletion affected sibling", response.Code, response.Body.String())
			}
			// Removing an empty child makes its parent deletable without recursion.
			emptyParent, err := s.Create(ctx, "alice", collection.ID, dms.CreateResource{Kind: "folder", Name: "Empty parent"})
			if err != nil {
				t.Fatal(err)
			}
			emptyChild, err := s.Create(ctx, "alice", emptyParent.ID, dms.CreateResource{Kind: "folder", Name: "Empty child"})
			if err != nil {
				t.Fatal(err)
			}
			response = request(h, "DELETE", "/v1/resources/"+emptyParent.ID, "", testToken, `"1"`, "")
			if response.Code != 409 {
				t.Fatal("ordinary delete ignored an empty child folder", response.Code, response.Body.String())
			}
			for _, id := range []string{emptyChild.ID, emptyParent.ID} {
				response = request(h, "DELETE", "/v1/resources/"+id, "", testToken, `"1"`, "")
				if response.Code != 204 {
					t.Fatal("empty folder required recursion", response.Code, response.Body.String())
				}
				if response = request(h, "GET", "/v1/resources/"+id, "", testToken, "", ""); response.Code != 404 {
					t.Fatal("deleted empty folder remained visible", response.Code, response.Body.String())
				}
			}
		})
	}
}
