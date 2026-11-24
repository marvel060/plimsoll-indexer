package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/plimsoll-protocol/plimsoll-indexer/internal/coverage"
	"github.com/plimsoll-protocol/plimsoll-indexer/internal/stellartoml"
)

// homeDomainSource is the Horizon surface TomlSync needs: the issuer account's
// home_domain.
type homeDomainSource interface {
	HomeDomain(ctx context.Context, issuer string) (string, error)
}

// tomlFetcher fetches and parses an issuer's SEP-1 stellar.toml.
type tomlFetcher interface {
	Fetch(ctx context.Context, domain, code, issuer string) (stellartoml.Info, error)
}

// tomlStore is the storage surface TomlSync needs.
type tomlStore interface {
	ListAssets(ctx context.Context) ([]coverage.Asset, error)
	SaveToml(ctx context.Context, sac string, info []byte, lookupErr string, at time.Time) error
}

// TomlSync records what each issuer's stellar.toml says about its asset,
// including the SEP-1 attestation_of_reserve link.
type TomlSync struct {
	Horizon homeDomainSource
	Fetcher tomlFetcher
	Store   tomlStore
	// MaxAge is how long a lookup result is kept before it is refreshed.
	MaxAge time.Duration
	Log    *slog.Logger
	// Now returns the current time. Tests inject a fixed clock.
	Now func() time.Time
}

func (t *TomlSync) Run(ctx context.Context) error {
	assets, err := t.Store.ListAssets(ctx)
	if err != nil {
		return fmt.Errorf("list assets: %w", err)
	}
	now := t.Now().UTC()
	for _, a := range assets {
		if a.TomlCheckedAt != nil && now.Sub(*a.TomlCheckedAt) < t.MaxAge {
			continue
		}
		info, lookupErr := t.lookup(ctx, a.Code, a.Issuer)
		var body []byte
		msg := ""
		if lookupErr != nil {
			msg = lookupErr.Error()
		}
		// Keep whatever we learned (e.g. home_domain) even on partial failure.
		if info.HomeDomain != "" || lookupErr == nil {
			body, _ = json.Marshal(info)
		}
		if err := t.Store.SaveToml(ctx, a.SAC, body, msg, now); err != nil {
			return fmt.Errorf("save toml for %s: %w", a.Code, err)
		}
		if lookupErr != nil {
			t.Log.Debug("stellar.toml lookup failed", "code", a.Code, "issuer", a.Issuer, "err", lookupErr)
		}
	}
	return nil
}

func (t *TomlSync) lookup(ctx context.Context, code, issuer string) (stellartoml.Info, error) {
	domain, err := t.Horizon.HomeDomain(ctx, issuer)
	if err != nil {
		return stellartoml.Info{}, err
	}
	return t.Fetcher.Fetch(ctx, domain, code, issuer)
}
