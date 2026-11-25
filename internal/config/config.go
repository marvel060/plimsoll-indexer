// Package config loads indexer settings from environment variables.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/stellar/go-stellar-sdk/keypair"
)

const (
	TestnetRPC        = "https://soroban-testnet.stellar.org"
	TestnetHorizon    = "https://horizon-testnet.stellar.org"
	TestnetPassphrase = "Test SDF Network ; September 2015"

	MainnetHorizon    = "https://horizon.stellar.org"
	MainnetPassphrase = "Public Global Stellar Network ; September 2015"

	StandaloneRPC        = "http://localhost:8000/rpc"
	StandaloneHorizon    = "http://localhost:8000"
	StandalonePassphrase = "Standalone Network ; February 2017"
)

// Preset is a named set of network defaults. Each field is only a default:
// STELLAR_RPC_URL, HORIZON_URL and STELLAR_NETWORK_PASSPHRASE override it.
type Preset struct {
	RPCURL     string
	HorizonURL string
	Passphrase string
}

// Presets are selected with the NETWORK variable. Mainnet has no RPC default
// because SDF does not run a public Soroban RPC for it: STELLAR_RPC_URL must
// be set to a provider's endpoint.
var Presets = map[string]Preset{
	"testnet":    {RPCURL: TestnetRPC, HorizonURL: TestnetHorizon, Passphrase: TestnetPassphrase},
	"mainnet":    {RPCURL: "", HorizonURL: MainnetHorizon, Passphrase: MainnetPassphrase},
	"standalone": {RPCURL: StandaloneRPC, HorizonURL: StandaloneHorizon, Passphrase: StandalonePassphrase},
}

type Config struct {
	// Network is the preset name the defaults below came from.
	Network     string
	DatabaseURL string
	Port        string
	RPCURL      string
	HorizonURL  string
	// HorizonCheckURL is a second, independent Horizon the poster compares the
	// supply against before signing it on chain. One Horizon is one point of
	// trust for the only figure this indexer asserts itself. Empty disables the
	// comparison.
	HorizonCheckURL      string
	NetworkPassphrase    string
	CoverageLedgerID     string
	ReporterRegistryID   string
	StartLedger          uint32
	PosterSecret         string
	WebhookAdminToken    string
	WebhookEncryptionKey string
	IngestInterval       time.Duration
	PostInterval         time.Duration
	// ReconcileInterval is how often contract state is read back to repair
	// what event ingestion missed. Each pass is a handful of simulations per
	// asset, so it can be infrequent.
	ReconcileInterval time.Duration
	RepostAfter       time.Duration
	TomlInterval      time.Duration
	CORSOrigins       []string
	LogLevel          string
	// KeepAwakeURL is requested every KeepAwakeInterval so a host that sleeps
	// idle services (Render's free tier) keeps this one, and its poster, up.
	KeepAwakeURL      string
	KeepAwakeInterval time.Duration

	// MaxFeeStroops is the absolute ceiling the invoker will sign per
	// transaction. Zero means no ceiling.
	MaxFeeStroops int64
	// InclusionFee is the per-operation base fee bid in stroops. Zero means
	// MinBaseFee (100 stroops).
	InclusionFee int64
	// ResourceFeeMarginPct is the percentage added to the simulated resource
	// fee as headroom. Zero means no margin.
	ResourceFeeMarginPct int64
	// PosterLowBalanceStroops is the XLM balance (in stroops) below which the
	// poster logs a warning on every tick. Zero disables the check.
	PosterLowBalanceStroops int64
}

// Field is one environment variable's entry in a Report.
type Field struct {
	Name     string
	Type     string // expected type, suffixed with "(required)" when required
	Value    string // current effective value; "<unset>" or "<redacted>" if hidden
	Secret   bool   // whether the real value is withheld from Value
	Required bool
	Err      error // nil when the value is valid
}

// Report is the outcome of validating every environment variable the indexer
// reads. It is built without any I/O, so startup can print it before opening
// the database or listening on a port.
type Report struct {
	Fields []Field
	errs   []error
}

// OK reports whether every variable is valid.
func (r Report) OK() bool { return len(r.errs) == 0 }

// Err joins every validation error. It is nil when all variables are valid.
func (r Report) Err() error { return errors.Join(r.errs...) }

// Table renders the report as a four-column table: variable, expected type,
// current value and error. Secret values are shown as "<redacted>".
func (r Report) Table() string {
	header := []string{"VARIABLE", "EXPECTED", "VALUE", "ERROR"}
	rows := make([][]string, 0, len(r.Fields)+1)
	rows = append(rows, header)
	for _, f := range r.Fields {
		message := ""
		if f.Err != nil {
			message = f.Err.Error()
		}
		rows = append(rows, []string{f.Name, f.Type, f.Value, message})
	}

	widths := make([]int, len(header))
	for _, row := range rows {
		for i, cell := range row {
			if n := len([]rune(cell)); n > widths[i] {
				widths[i] = n
			}
		}
	}

	var b strings.Builder
	write := func(cells []string) {
		for i, cell := range cells {
			b.WriteString(cell)
			if i < len(cells)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-len([]rune(cell))+2))
			}
		}
		b.WriteByte('\n')
	}
	write(header)
	rule := make([]string, len(header))
	for i := range rule {
		rule[i] = strings.Repeat("-", widths[i])
	}
	write(rule)
	for _, row := range rows[1:] {
		write(row)
	}
	return b.String()
}

// add records a variable in the report and returns its index, which fail and
// mark take.
func (r *Report) add(name, typ, value string, required, secret bool) int {
	display := value
	switch {
	case value == "":
		display = "<unset>"
	case secret:
		display = "<redacted>"
	}
	if required {
		typ += " (required)"
	}
	r.Fields = append(r.Fields, Field{
		Name:     name,
		Type:     typ,
		Value:    truncate(display, 48),
		Secret:   secret,
		Required: required,
	})
	return len(r.Fields) - 1
}

// fail records err against field i and adds it to the report's error.
func (r *Report) fail(i int, err error) {
	r.Fields[i].Err = err
	r.errs = append(r.errs, err)
}

// mark records err against field i without adding it to the report's error.
// Used when one problem concerns several variables and the error message names
// them all.
func (r *Report) mark(i int, err error) { r.Fields[i].Err = err }

// Load reads and validates the environment. Missing required values are
// reported together.
func Load() (Config, error) {
	c, report := Validate()
	return c, report.Err()
}

// Validate reads the environment and checks every variable the indexer reads.
// It performs no I/O, so callers can inspect and print the Report before
// touching the database or network. The returned Config holds every value that
// could be parsed, even when the report contains errors.
func Validate() (Config, Report) {
	var rep Report

	network := strings.ToLower(envOr("NETWORK", "testnet"))
	preset, knownNetwork := Presets[network]
	if !knownNetwork {
		preset = Presets["testnet"]
	}

	c := Config{
		Network:              network,
		DatabaseURL:          os.Getenv("DATABASE_URL"),
		Port:                 envOr("PORT", "8080"),
		RPCURL:               envOr("STELLAR_RPC_URL", preset.RPCURL),
		HorizonURL:           strings.TrimRight(envOr("HORIZON_URL", preset.HorizonURL), "/"),
		HorizonCheckURL:      strings.TrimRight(os.Getenv("HORIZON_CHECK_URL"), "/"),
		NetworkPassphrase:    envOr("STELLAR_NETWORK_PASSPHRASE", preset.Passphrase),
		CoverageLedgerID:     os.Getenv("COVERAGE_LEDGER_ID"),
		ReporterRegistryID:   os.Getenv("REPORTER_REGISTRY_ID"),
		PosterSecret:         os.Getenv("SUPPLY_POSTER_SECRET"),
		WebhookAdminToken:    os.Getenv("WEBHOOK_ADMIN_TOKEN"),
		WebhookEncryptionKey: os.Getenv("WEBHOOK_ENCRYPTION_KEY"),
		LogLevel:             strings.ToLower(envOr("LOG_LEVEL", "info")),
		KeepAwakeURL:         keepAwakeURL(os.Getenv("KEEP_AWAKE_URL"), os.Getenv("RENDER_EXTERNAL_URL")),
	}

	i := rep.add("NETWORK", "enum: testnet | mainnet | standalone", c.Network, false, false)
	if !knownNetwork {
		rep.fail(i, fmt.Errorf("NETWORK %q is not one of testnet, mainnet, standalone", c.Network))
	}

	i = rep.add("DATABASE_URL", "Postgres URL", c.DatabaseURL, true, true)
	if c.DatabaseURL == "" {
		rep.fail(i, errors.New("DATABASE_URL is required"))
	}

	i = rep.add("PORT", "port (1-65535)", c.Port, false, false)
	if n, err := strconv.Atoi(c.Port); err != nil || n < 1 || n > 65535 {
		rep.fail(i, fmt.Errorf("PORT %q is not a port number between 1 and 65535", c.Port))
	}

	i = rep.add("STELLAR_RPC_URL", "URL", c.RPCURL, false, false)
	if c.RPCURL == "" {
		rep.fail(i, fmt.Errorf("STELLAR_RPC_URL is required for NETWORK=%s", c.Network))
	} else if err := validateURL(c.RPCURL); err != nil {
		rep.fail(i, fmt.Errorf("STELLAR_RPC_URL: %w", err))
	}

	i = rep.add("HORIZON_URL", "URL", c.HorizonURL, false, false)
	if c.HorizonURL == "" {
		rep.fail(i, errors.New("HORIZON_URL is required"))
	} else if err := validateURL(c.HorizonURL); err != nil {
		rep.fail(i, fmt.Errorf("HORIZON_URL: %w", err))
	}

	i = rep.add("HORIZON_CHECK_URL", "URL; empty disables the cross-check", c.HorizonCheckURL, false, false)
	if c.HorizonCheckURL != "" {
		if err := validateURL(c.HorizonCheckURL); err != nil {
			rep.fail(i, fmt.Errorf("HORIZON_CHECK_URL: %w", err))
		}
	}

	i = rep.add("STELLAR_NETWORK_PASSPHRASE", "string", c.NetworkPassphrase, false, false)
	if c.NetworkPassphrase == "" {
		rep.fail(i, errors.New("STELLAR_NETWORK_PASSPHRASE is required"))
	}

	i = rep.add("COVERAGE_LEDGER_ID", "contract ID", c.CoverageLedgerID, true, false)
	if c.CoverageLedgerID == "" {
		rep.fail(i, errors.New("COVERAGE_LEDGER_ID is required"))
	}

	i = rep.add("REPORTER_REGISTRY_ID", "contract ID", c.ReporterRegistryID, true, false)
	if c.ReporterRegistryID == "" {
		rep.fail(i, errors.New("REPORTER_REGISTRY_ID is required"))
	}

	startValue := envOr("START_LEDGER", "0")
	i = rep.add("START_LEDGER", "uint32", startValue, false, false)
	start, err := strconv.ParseUint(startValue, 10, 32)
	if err != nil {
		rep.fail(i, fmt.Errorf("START_LEDGER %q is not a uint32", startValue))
	} else {
		c.StartLedger = uint32(start)
	}

	i = rep.add("SUPPLY_POSTER_SECRET", "Stellar secret seed", c.PosterSecret, false, true)
	if c.PosterSecret != "" {
		if _, err := keypair.ParseFull(c.PosterSecret); err != nil {
			rep.fail(i, errors.New("SUPPLY_POSTER_SECRET is not a valid secret seed"))
		}
	}

	adminIdx := rep.add("WEBHOOK_ADMIN_TOKEN", "string", c.WebhookAdminToken, false, true)
	keyIdx := rep.add("WEBHOOK_ENCRYPTION_KEY", "base64-encoded 32-byte key", c.WebhookEncryptionKey, false, true)
	if (c.WebhookAdminToken == "") != (c.WebhookEncryptionKey == "") {
		err := errors.New("WEBHOOK_ADMIN_TOKEN and WEBHOOK_ENCRYPTION_KEY must be configured together")
		rep.fail(adminIdx, err)
		rep.mark(keyIdx, err)
	}
	if c.WebhookEncryptionKey != "" {
		key, err := base64.StdEncoding.DecodeString(c.WebhookEncryptionKey)
		if err != nil || len(key) != 32 {
			rep.fail(keyIdx, errors.New("WEBHOOK_ENCRYPTION_KEY must be base64-encoded 32-byte key"))
		}
	}

	durations := []struct {
		name string
		def  string
		dst  *time.Duration
	}{
		{"INGEST_INTERVAL", "10s", &c.IngestInterval},
		{"POST_INTERVAL", "15m", &c.PostInterval},
		{"RECONCILE_INTERVAL", "15m", &c.ReconcileInterval},
		{"REPOST_AFTER", "6h", &c.RepostAfter},
		{"TOML_INTERVAL", "6h", &c.TomlInterval},
		{"KEEP_AWAKE_INTERVAL", "10m", &c.KeepAwakeInterval},
	}
	for _, d := range durations {
		value := envOr(d.name, d.def)
		i := rep.add(d.name, "duration", value, false, false)
		v, err := time.ParseDuration(value)
		if err != nil {
			rep.fail(i, fmt.Errorf("%s %q is not a duration (e.g. 10s, 15m, 6h)", d.name, value))
			continue
		}
		if v <= 0 {
			rep.fail(i, fmt.Errorf("%s %q must be positive", d.name, value))
			continue
		}
		*d.dst = v
	}

	i = rep.add("KEEP_AWAKE_URL", `URL or "none"`, c.KeepAwakeURL, false, false)
	if c.KeepAwakeURL != "" {
		if err := validateURL(c.KeepAwakeURL); err != nil {
			rep.fail(i, fmt.Errorf("KEEP_AWAKE_URL: %w", err))
		}
	}

	int64s := []struct {
		name string
		dst  *int64
	}{
		{"MAX_FEE_STROOPS", &c.MaxFeeStroops},
		{"INCLUSION_FEE", &c.InclusionFee},
		{"RESOURCE_FEE_MARGIN_PCT", &c.ResourceFeeMarginPct},
		{"POSTER_LOW_BALANCE_STROOPS", &c.PosterLowBalanceStroops},
	}
	for _, f := range int64s {
		value := os.Getenv(f.name)
		i := rep.add(f.name, "non-negative int64", value, false, false)
		if value == "" {
			continue
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 0 {
			rep.fail(i, fmt.Errorf("%s %q is not a non-negative int64", f.name, value))
			continue
		}
		*f.dst = n
	}

	corsValue := envOr("CORS_ORIGINS", "*")
	i = rep.add("CORS_ORIGINS", "comma-separated origins or *", corsValue, false, false)
	for _, origin := range strings.Split(corsValue, ",") {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}
		if origin != "*" {
			if err := validateURL(origin); err != nil {
				rep.fail(i, fmt.Errorf("CORS_ORIGINS entry %q is not \"*\" or an http(s) origin: %w", origin, err))
				continue
			}
		}
		c.CORSOrigins = append(c.CORSOrigins, origin)
	}
	if len(c.CORSOrigins) == 0 {
		rep.fail(i, errors.New("CORS_ORIGINS has no origins"))
	}

	i = rep.add("LOG_LEVEL", "enum: debug | info | warn | error", c.LogLevel, false, false)
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		rep.fail(i, fmt.Errorf("LOG_LEVEL %q is not one of debug, info, warn, error", c.LogLevel))
	}

	return c, rep
}

// keepAwakeURL picks the URL to ping: KEEP_AWAKE_URL if set ("none" turns
// it off), otherwise Render's RENDER_EXTERNAL_URL plus /healthz, otherwise
// nothing.
func keepAwakeURL(explicit, renderExternal string) string {
	switch {
	case explicit == "none":
		return ""
	case explicit != "":
		return explicit
	case renderExternal != "":
		return strings.TrimRight(renderExternal, "/") + "/healthz"
	default:
		return ""
	}
}

// validateURL checks that raw is an absolute http(s) URL with a host.
func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("must be an absolute http:// or https:// URL")
	}
	if u.Host == "" {
		return errors.New("must include a host")
	}
	return nil
}

// truncate shortens s to at most n runes, marking the cut with "...".
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
}

// PostingEnabled reports whether this instance should post supply snapshots.
func (c Config) PostingEnabled() bool { return c.PosterSecret != "" }

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
