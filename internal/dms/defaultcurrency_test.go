package dms

import (
	"errors"
	"testing"
)

func TestWorkspaceDefaultCurrencyIsManagedAndValidated(t *testing.T) {
	s, workspace, list := fixture(t)
	updated, err := s.Update(testContext, "alice", workspace.ID, workspace.Version, UpdateResource{DefaultCurrency: ptr("EUR")})
	if err != nil || updated.DefaultCurrency != "EUR" {
		t.Fatal(updated, err)
	}
	if _, err = s.Update(testContext, "alice", workspace.ID, workspace.Version, UpdateResource{DefaultCurrency: ptr("JPY")}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatal("stale write accepted", err)
	}
	for _, value := range []string{"eur", "EURO", "E1R"} {
		if _, err = s.Update(testContext, "alice", workspace.ID, updated.Version, UpdateResource{DefaultCurrency: &value}); err == nil {
			t.Fatal("invalid currency accepted", value)
		}
	}
	if _, err = s.Update(testContext, "bob", workspace.ID, updated.Version, UpdateResource{DefaultCurrency: ptr("JPY")}); err == nil {
		t.Fatal("non-manager changed currency")
	}
	if _, err = s.Update(testContext, "alice", list.ID, list.Version, UpdateResource{DefaultCurrency: ptr("JPY")}); err == nil {
		t.Fatal("list accepted workspace setting")
	}
	if _, err = s.Client.Resource.UpdateOneID(list.ID).SetDefaultCurrency("JPY").Save(testContext); err == nil {
		t.Fatal("database accepted default currency on list")
	}
	updated, err = s.Update(testContext, "alice", workspace.ID, updated.Version, UpdateResource{DefaultCurrency: ptr("")})
	if err != nil || updated.DefaultCurrency != "" {
		t.Fatal("could not inherit organization default", updated, err)
	}
}
