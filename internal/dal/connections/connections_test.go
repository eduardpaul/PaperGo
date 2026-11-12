package connections_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"papergo/internal/dal"
	"papergo/internal/dal/connections"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
)

func TestConnections(t *testing.T) {
	daltest.Engines(t, func(t *testing.T, newStore func(testing.TB) *dal.Store) {
		ctx := context.Background()
		s := newStore(t)
		ws, err := s.Resources().Create(ctx, resources.New{ID: "workspace", Kind: refs.KindWorkspace, Name: "Home"})
		if err != nil {
			t.Fatal(err)
		}
		in := connections.Connection{WorkspaceID: ws.ID, Key: "home", Name: "Home broker", Kind: connections.KindMQTT, Enabled: true,
			ConfigCiphertext: []byte{1, 2, 3}, ConfigHash: "hash", Settings: json.RawMessage(`{}`),
			Channels: json.RawMessage(`[{"key":"sensors","topics":"home/+/sensor/#","uses":["see"]}]`), CreatedBy: "alice"}

		c, err := s.Connections().Create(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.Connections().Find(ctx, c.ID)
		var channels []map[string]any
		if err != nil || got.Version != 1 || got.Kind != connections.KindMQTT || !got.Enabled || string(got.ConfigCiphertext) != "\x01\x02\x03" || json.Unmarshal(got.Channels, &channels) != nil || channels[0]["key"] != "sensors" || got.UpdatedBy != "alice" {
			t.Fatalf("stored: %+v %v", got, err)
		}
		if _, err := s.Connections().Create(ctx, in); !errors.Is(err, dberr.ErrUniqueViolation) {
			t.Fatalf("a second connection with the key: %v", err)
		}
		other := in
		other.Key, other.Name, other.WorkspaceID = "other", "Other", "missing"
		if _, err := s.Connections().Create(ctx, other); !errors.Is(err, dberr.ErrInvalidContainment) {
			t.Fatalf("a connection outside a workspace: %v", err)
		}
		other.WorkspaceID, other.Kind = ws.ID, "ftp"
		if _, err := s.Connections().Create(ctx, other); !errors.Is(err, dberr.ErrCheckViolation) {
			t.Fatalf("an unknown kind: %v", err)
		}
		other.Kind = connections.KindHTTP
		if _, err := s.Connections().Create(ctx, other); err != nil {
			t.Fatal(err)
		}

		next := got
		next.Name, next.Enabled, next.Key, next.Kind = "Broker", false, "changed", connections.KindHTTP
		replaced, err := s.Connections().Replace(ctx, c.ID, 1, "bob", next)
		if err != nil || replaced.Version != 2 || replaced.Key != "home" || replaced.Kind != connections.KindMQTT || replaced.Enabled || replaced.UpdatedBy != "bob" || replaced.CreatedBy != "alice" {
			t.Fatalf("replaced: %+v %v", replaced, err)
		}
		if _, err := s.Connections().Replace(ctx, c.ID, 1, "bob", next); !errors.Is(err, dberr.ErrConcurrentUpdate) {
			t.Fatalf("a stale replace: %v", err)
		}
		if list, err := s.Connections().List(ctx, connections.Filter{WorkspaceIDs: []string{ws.ID}}); err != nil || len(list) != 2 || list[0].Name != "Broker" {
			t.Fatalf("by name: %+v %v", list, err)
		}
		if list, err := s.Connections().List(ctx, connections.Filter{Enabled: true}); err != nil || len(list) != 1 || list[0].Kind != connections.KindHTTP {
			t.Fatalf("enabled: %+v %v", list, err)
		}
		if list, err := s.Connections().List(ctx, connections.Filter{WorkspaceIDs: []string{ws.ID}, Key: "home"}); err != nil || len(list) != 1 {
			t.Fatalf("by key: %+v %v", list, err)
		}
		if err := s.Connections().Delete(ctx, c.ID, 1); !errors.Is(err, dberr.ErrConcurrentUpdate) {
			t.Fatalf("a stale delete: %v", err)
		}
		if err := s.Connections().Delete(ctx, c.ID, 2); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Connections().Find(ctx, c.ID); !dberr.IsNotFound(err) {
			t.Fatalf("deleted: %v", err)
		}
	})
}
