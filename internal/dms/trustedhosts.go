package dms

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"papergo/internal/integration"
	"sort"

	"papergo/internal/dal/dberr"
	"papergo/internal/dal/integrations"
	"papergo/internal/dal/settings"
	"papergo/internal/hosts"
)

// TrustedHosts is the organization's list of outside hosts that PaperGo
// fetches from and shows without asking. Version is 0 until it is changed.
type TrustedHosts struct {
	Hosts   []string `json:"hosts"`
	Version int64    `json:"version"`
}

type trustedHostsValue struct {
	Hosts []string `json:"hosts"`
}

// TrustedHosts returns the organization's trusted hosts; every active
// account may read them, since the web app needs them to show documents.
func (s *Service) TrustedHosts(ctx context.Context) (TrustedHosts, error) {
	stored, found, err := s.DAL.Settings().Find(ctx, settings.KeyTrustedHosts, "")
	if err != nil || !found {
		return TrustedHosts{Hosts: hosts.Default()}, err
	}
	var v trustedHostsValue
	if err = json.Unmarshal(stored.Value, &v); err != nil {
		return TrustedHosts{}, err
	}
	return TrustedHosts{Hosts: v.Hosts, Version: stored.Version}, nil
}

// SetTrustedHosts replaces the organization's trusted hosts when their
// version is still version; it needs the organization.manage scope.
func (s *Service) SetTrustedHosts(ctx context.Context, subject string, version int64, entries []string) (TrustedHosts, error) {
	if err := s.RequireScope(ctx, subject, "organization.manage"); err != nil {
		return TrustedHosts{}, err
	}
	list, err := hosts.List(entries)
	if err != nil {
		return TrustedHosts{}, invalid(err.Error())
	}
	raw, err := json.Marshal(trustedHostsValue{Hosts: list})
	if err != nil {
		return TrustedHosts{}, err
	}
	err = s.write(ctx, func(t *Service) error {
		if _, err := t.DAL.Settings().Put(ctx, settings.KeyTrustedHosts, "", raw, version, subject); err != nil {
			if errors.Is(err, dberr.ErrConcurrentUpdate) {
				return ErrPreconditionFailed
			}
			return err
		}
		return t.RecordEntity(ctx, subject, "settings.trusted_hosts", AuditTarget{}, map[string]any{"hosts": list})
	})
	if err != nil {
		return TrustedHosts{}, err
	}
	return s.TrustedHosts(ctx)
}

// trustedHost returns the check fetches make of each host they reach.
func (s *Service) trustedHost(ctx context.Context) (func(host string) bool, error) {
	list, err := s.TrustedHosts(ctx)
	if err != nil {
		return nil, err
	}
	return func(host string) bool { return hosts.Trusted(list.Hosts, host) }, nil
}

// HostUse is an outside host that configured features reach, with how
// many uses of each feature reach it, such as integrations or connections.
type HostUse struct {
	Host string         `json:"host"`
	Uses map[string]int `json:"uses"`
}

// HostUsesIntegrations names list integrations among a host's uses.
const HostUsesIntegrations = "integrations"

// TrustedHostUsage returns the hosts that list integrations and the
// features that report theirs reach, and how many uses each has, so
// administrators see what a narrower list stops. It needs the
// organization.manage scope.
func (s *Service) TrustedHostUsage(ctx context.Context, subject string) ([]HostUse, error) {
	if err := s.RequireScope(ctx, subject, "organization.manage"); err != nil {
		return nil, err
	}
	counts := map[string]map[string]int{}
	add := func(feature, host string, n int) {
		if host == "" || n == 0 {
			return
		}
		if counts[host] == nil {
			counts[host] = map[string]int{}
		}
		counts[host][feature] += n
	}
	if s.integrations != nil {
		rows, err := s.DAL.Integrations().List(ctx, integrations.Filter{})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			add(HostUsesIntegrations, s.integrationHost(row), 1)
		}
	}
	for _, r := range s.ext.hosts {
		hosts, err := r.report(ctx)
		if err != nil {
			return nil, err
		}
		for host, n := range hosts {
			add(r.feature, host, n)
		}
	}
	out := make([]HostUse, 0, len(counts))
	for host, uses := range counts {
		out = append(out, HostUse{Host: host, Uses: uses})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out, nil
}

// integrationHost is the host an integration fetches from, read from its
// decrypted configuration; empty when it cannot be read.
func (s *Service) integrationHost(row integrations.Integration) string {
	plain, err := s.integrations.secrets.Decrypt(integration.PurposeListIntegration, row.ContainerID, row.ConfigCiphertext)
	if err != nil {
		return ""
	}
	var config iCalendarConfig
	if json.Unmarshal([]byte(plain), &config) != nil {
		return ""
	}
	u, err := url.Parse(config.URL)
	if err != nil {
		return ""
	}
	return hosts.Key(u.Hostname())
}
