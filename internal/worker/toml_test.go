package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/plimsoll-protocol/plimsoll-indexer/internal/coverage"
	"github.com/plimsoll-protocol/plimsoll-indexer/internal/stellartoml"
)

var tomlNow = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

type fakeHomeDomains struct {
	domain  string
	err     error
	issuers []string
}

func (f *fakeHomeDomains) HomeDomain(_ context.Context, issuer string) (string, error) {
	f.issuers = append(f.issuers, issuer)
	return f.domain, f.err
}

type fakeTomlFetcher struct {
	info    stellartoml.Info
	err     error
	domains []string
}

func (f *fakeTomlFetcher) Fetch(_ context.Context, domain, _, _ string) (stellartoml.Info, error) {
	f.domains = append(f.domains, domain)
	return f.info, f.err
}

// fakeTomlStore records saves and emulates the UPDATE's COALESCE: a nil info
// body leaves the previously stored toml in place.
type fakeTomlStore struct {
	assets []coverage.Asset
	toml   map[string][]byte
	errs   map[string]string
	at     map[string]time.Time
	saves  int
}

func newFakeTomlStore(assets ...coverage.Asset) *fakeTomlStore {
	return &fakeTomlStore{
		assets: assets,
		toml:   map[string][]byte{},
		errs:   map[string]string{},
		at:     map[string]time.Time{},
	}
}

func (f *fakeTomlStore) ListAssets(context.Context) ([]coverage.Asset, error) {
	return f.assets, nil
}

func (f *fakeTomlStore) SaveToml(_ context.Context, sac string, info []byte, lookupErr string, at time.Time) error {
	f.saves++
	if info != nil {
		f.toml[sac] = info
	}
	f.errs[sac] = lookupErr
	f.at[sac] = at
	return nil
}

func newTomlSync(home *fakeHomeDomains, fetch *fakeTomlFetcher, st *fakeTomlStore) *TomlSync {
	return &TomlSync{
		Horizon: home,
		Fetcher: fetch,
		Store:   st,
		MaxAge:  6 * time.Hour,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:     func() time.Time { return tomlNow },
	}
}

func TestTomlSyncLooksUpFreshAsset(t *testing.T) {
	home := &fakeHomeDomains{domain: "acme.example"}
	fetch := &fakeTomlFetcher{info: stellartoml.Info{
		HomeDomain: "acme.example",
		OrgName:    "Acme",
		Currency:   &stellartoml.Currency{Code: "PUSD", Issuer: "GISSUER"},
	}}
	st := newFakeTomlStore(coverage.Asset{SAC: testSAC, Code: "PUSD", Issuer: "GISSUER"})

	if err := newTomlSync(home, fetch, st).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(home.issuers) != 1 || home.issuers[0] != "GISSUER" {
		t.Fatalf("home domain issuers = %v", home.issuers)
	}
	if len(fetch.domains) != 1 || fetch.domains[0] != "acme.example" {
		t.Fatalf("fetched domains = %v", fetch.domains)
	}
	var info stellartoml.Info
	if err := json.Unmarshal(st.toml[testSAC], &info); err != nil {
		t.Fatalf("stored toml %q: %v", st.toml[testSAC], err)
	}
	if info.OrgName != "Acme" || info.Currency == nil || info.Currency.Code != "PUSD" {
		t.Fatalf("stored info = %+v", info)
	}
	if st.errs[testSAC] != "" {
		t.Errorf("lookup error = %q", st.errs[testSAC])
	}
	if !st.at[testSAC].Equal(tomlNow) {
		t.Errorf("checked_at = %v want %v", st.at[testSAC], tomlNow)
	}
}

func TestTomlSyncSkipsRecentlyCheckedAsset(t *testing.T) {
	checked := tomlNow.Add(-time.Hour)
	home := &fakeHomeDomains{domain: "acme.example"}
	fetch := &fakeTomlFetcher{}
	st := newFakeTomlStore(coverage.Asset{
		SAC: testSAC, Code: "PUSD", Issuer: "GISSUER", TomlCheckedAt: &checked,
	})

	if err := newTomlSync(home, fetch, st).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(home.issuers) != 0 || len(fetch.domains) != 0 {
		t.Fatalf("looked up a fresh asset: issuers=%v domains=%v", home.issuers, fetch.domains)
	}
	if st.saves != 0 {
		t.Fatalf("saved %d times for a recently checked asset", st.saves)
	}
}

func TestTomlSyncRecordsIssuerWithoutHomeDomain(t *testing.T) {
	home := &fakeHomeDomains{domain: ""}
	// The real fetcher returns the empty home domain alongside the error.
	fetch := &fakeTomlFetcher{info: stellartoml.Info{}, err: errors.New("issuer has no home_domain")}
	st := newFakeTomlStore(coverage.Asset{SAC: testSAC, Code: "PUSD", Issuer: "GISSUER"})

	if err := newTomlSync(home, fetch, st).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.errs[testSAC], "issuer has no home_domain") {
		t.Fatalf("toml_error = %q", st.errs[testSAC])
	}
	if st.toml[testSAC] != nil {
		t.Errorf("stored toml = %q, want none", st.toml[testSAC])
	}
	if !st.at[testSAC].Equal(tomlNow) {
		t.Errorf("checked_at = %v want %v", st.at[testSAC], tomlNow)
	}
}

func TestTomlSyncFetchFailureKeepsStoredToml(t *testing.T) {
	previous := []byte(`{"home_domain":"acme.example","org_name":"Acme"}`)
	stale := tomlNow.Add(-24 * time.Hour)
	home := &fakeHomeDomains{domain: "acme.example"}
	fetch := &fakeTomlFetcher{err: errors.New("fetch stellar.toml: status 500")}
	st := newFakeTomlStore(coverage.Asset{
		SAC: testSAC, Code: "PUSD", Issuer: "GISSUER", TomlCheckedAt: &stale,
	})
	st.toml[testSAC] = previous

	if err := newTomlSync(home, fetch, st).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if string(st.toml[testSAC]) != string(previous) {
		t.Fatalf("stored toml = %q, want previous %q", st.toml[testSAC], previous)
	}
	if !strings.Contains(st.errs[testSAC], "status 500") {
		t.Errorf("toml_error = %q", st.errs[testSAC])
	}
	if !st.at[testSAC].Equal(tomlNow) {
		t.Errorf("checked_at = %v want %v", st.at[testSAC], tomlNow)
	}
}
