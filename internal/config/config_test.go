package config

import (
	"os"
	"strings"
	"testing"
)

func TestKeepAwakeURL(t *testing.T) {
	cases := []struct {
		explicit, render, want string
	}{
		{"", "", ""},
		{"", "https://plimsoll-indexer.onrender.com", "https://plimsoll-indexer.onrender.com/healthz"},
		{"", "https://x.onrender.com/", "https://x.onrender.com/healthz"},
		{"https://example.org/ping", "https://x.onrender.com", "https://example.org/ping"},
		{"none", "https://x.onrender.com", ""},
	}
	for _, c := range cases {
		if got := keepAwakeURL(c.explicit, c.render); got != c.want {
			t.Errorf("keepAwakeURL(%q, %q) = %q, want %q", c.explicit, c.render, got, c.want)
		}
	}
}

func setBase(t *testing.T) {
	t.Helper()
	for _, k := range []string{"NETWORK", "STELLAR_RPC_URL", "HORIZON_URL", "STELLAR_NETWORK_PASSPHRASE", "WEBHOOK_ADMIN_TOKEN", "WEBHOOK_ENCRYPTION_KEY"} {
		t.Setenv(k, "")
	}
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("COVERAGE_LEDGER_ID", "C1")
	t.Setenv("REPORTER_REGISTRY_ID", "C2")
}

func TestWebhookConfigurationMustBeComplete(t *testing.T) {
	setBase(t)
	t.Setenv("WEBHOOK_ADMIN_TOKEN", "admin")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "must be configured together") {
		t.Fatalf("want paired webhook configuration error, got %v", err)
	}

	setBase(t)
	t.Setenv("WEBHOOK_ENCRYPTION_KEY", "not-base64")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "must be configured together") {
		t.Fatalf("want paired webhook configuration error, got %v", err)
	}

	setBase(t)
	t.Setenv("WEBHOOK_ADMIN_TOKEN", "admin")
	t.Setenv("WEBHOOK_ENCRYPTION_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if _, err := Load(); err != nil {
		t.Fatalf("valid webhook configuration: %v", err)
	}

	t.Setenv("WEBHOOK_ENCRYPTION_KEY", "short")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "32-byte key") {
		t.Fatalf("want invalid key error, got %v", err)
	}
}

func TestNetworkPresets(t *testing.T) {
	cases := []struct {
		network, rpc, horizon, pass string
	}{
		{"", TestnetRPC, TestnetHorizon, TestnetPassphrase},
		{"testnet", TestnetRPC, TestnetHorizon, TestnetPassphrase},
		{"TestNet", TestnetRPC, TestnetHorizon, TestnetPassphrase},
		{"standalone", StandaloneRPC, StandaloneHorizon, StandalonePassphrase},
	}
	for _, c := range cases {
		setBase(t)
		t.Setenv("NETWORK", c.network)
		got, err := Load()
		if err != nil {
			t.Fatalf("NETWORK=%q: %v", c.network, err)
		}
		if got.RPCURL != c.rpc || got.HorizonURL != c.horizon || got.NetworkPassphrase != c.pass {
			t.Errorf("NETWORK=%q: got %q %q %q", c.network, got.RPCURL, got.HorizonURL, got.NetworkPassphrase)
		}
	}
}

func TestNetworkPresetOverrides(t *testing.T) {
	setBase(t)
	t.Setenv("NETWORK", "standalone")
	t.Setenv("STELLAR_RPC_URL", "http://rpc:9000/")
	t.Setenv("HORIZON_URL", "http://horizon:9001/")
	t.Setenv("STELLAR_NETWORK_PASSPHRASE", "custom")
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.RPCURL != "http://rpc:9000/" || got.HorizonURL != "http://horizon:9001" || got.NetworkPassphrase != "custom" {
		t.Errorf("overrides ignored: %+v", got)
	}
	if got.Network != "standalone" {
		t.Errorf("Network = %q", got.Network)
	}
}

func TestMainnetNeedsRPC(t *testing.T) {
	setBase(t)
	t.Setenv("NETWORK", "mainnet")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "STELLAR_RPC_URL is required") {
		t.Fatalf("want missing RPC error, got %v", err)
	}
	t.Setenv("STELLAR_RPC_URL", "https://rpc.example.org")
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.HorizonURL != MainnetHorizon || got.NetworkPassphrase != MainnetPassphrase {
		t.Errorf("mainnet defaults: %+v", got)
	}
}

func TestUnknownNetwork(t *testing.T) {
	setBase(t)
	t.Setenv("NETWORK", "futurenet")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "NETWORK") {
		t.Fatalf("want unknown network error, got %v", err)
	}
}

// TestLoadDefaults verifies that all optional fields carry their documented
// defaults when only the required variables are set.
func TestLoadDefaults(t *testing.T) {
	setBase(t)
	got, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Port != "8080" {
		t.Errorf("Port = %q, want 8080", got.Port)
	}
	if got.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", got.LogLevel)
	}
	if len(got.CORSOrigins) != 1 || got.CORSOrigins[0] != "*" {
		t.Errorf("CORSOrigins = %v, want [*]", got.CORSOrigins)
	}
	if got.IngestInterval.String() != "10s" {
		t.Errorf("IngestInterval = %v, want 10s", got.IngestInterval)
	}
	if got.PostInterval.String() != "15m0s" {
		t.Errorf("PostInterval = %v, want 15m0s", got.PostInterval)
	}
	if got.ReconcileInterval.String() != "15m0s" {
		t.Errorf("ReconcileInterval = %v, want 15m0s", got.ReconcileInterval)
	}
	if got.RepostAfter.String() != "6h0m0s" {
		t.Errorf("RepostAfter = %v, want 6h0m0s", got.RepostAfter)
	}
	if got.TomlInterval.String() != "6h0m0s" {
		t.Errorf("TomlInterval = %v, want 6h0m0s", got.TomlInterval)
	}
	if got.KeepAwakeInterval.String() != "10m0s" {
		t.Errorf("KeepAwakeInterval = %v, want 10m0s", got.KeepAwakeInterval)
	}
}

// TestLoadRequiredMissing checks that each required variable produces an error
// when absent, and that all missing variables are reported together.
func TestLoadRequiredMissing(t *testing.T) {
	required := []struct {
		key     string
		wantMsg string
	}{
		{"DATABASE_URL", "DATABASE_URL is required"},
		{"COVERAGE_LEDGER_ID", "COVERAGE_LEDGER_ID is required"},
		{"REPORTER_REGISTRY_ID", "REPORTER_REGISTRY_ID is required"},
	}

	// Each variable alone.
	for _, r := range required {
		t.Run(r.key, func(t *testing.T) {
			setBase(t)
			t.Setenv(r.key, "")
			_, err := Load()
			if err == nil {
				t.Fatalf("Load() = nil error, want error containing %q", r.wantMsg)
			}
			if !strings.Contains(err.Error(), r.wantMsg) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), r.wantMsg)
			}
		})
	}

	// All required variables absent at once — all messages must appear together.
	t.Run("all_missing", func(t *testing.T) {
		setBase(t)
		t.Setenv("DATABASE_URL", "")
		t.Setenv("COVERAGE_LEDGER_ID", "")
		t.Setenv("REPORTER_REGISTRY_ID", "")
		_, err := Load()
		if err == nil {
			t.Fatal("Load() = nil error, want combined error")
		}
		for _, r := range required {
			if !strings.Contains(err.Error(), r.wantMsg) {
				t.Errorf("combined error missing %q: %v", r.wantMsg, err)
			}
		}
	})
}

// TestLoadBadDurations checks that each malformed duration variable is reported.
func TestLoadBadDurations(t *testing.T) {
	durationVars := []string{
		"INGEST_INTERVAL",
		"POST_INTERVAL",
		"RECONCILE_INTERVAL",
		"REPOST_AFTER",
		"TOML_INTERVAL",
		"KEEP_AWAKE_INTERVAL",
	}
	for _, v := range durationVars {
		t.Run(v, func(t *testing.T) {
			setBase(t)
			t.Setenv(v, "notaduration")
			_, err := Load()
			if err == nil {
				t.Fatalf("Load() = nil error with bad %s", v)
			}
			if !strings.Contains(err.Error(), v) {
				t.Errorf("error = %q, want it to mention %q", err.Error(), v)
			}
		})
	}
}

// TestLoadCORSParsing checks that CORS_ORIGINS is split on commas and trimmed.
func TestLoadCORSParsing(t *testing.T) {
	cases := []struct {
		value string
		want  []string
	}{
		{"*", []string{"*"}},
		{"https://app.example.com", []string{"https://app.example.com"}},
		{"https://a.example.com,https://b.example.com", []string{"https://a.example.com", "https://b.example.com"}},
		{"  https://a.example.com ,  https://b.example.com  ", []string{"https://a.example.com", "https://b.example.com"}},
	}
	for _, c := range cases {
		t.Run(c.value, func(t *testing.T) {
			setBase(t)
			t.Setenv("CORS_ORIGINS", c.value)
			got, err := Load()
			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}
			if len(got.CORSOrigins) != len(c.want) {
				t.Fatalf("CORSOrigins = %v, want %v", got.CORSOrigins, c.want)
			}
			for i, o := range got.CORSOrigins {
				if o != c.want[i] {
					t.Errorf("CORSOrigins[%d] = %q, want %q", i, o, c.want[i])
				}
			}
		})
	}
}

// TestKeepAwakeURLIntegration tests all four KEEP_AWAKE_URL cases through Load.
func TestKeepAwakeURLIntegration(t *testing.T) {
	cases := []struct {
		name           string
		keepAwake      string
		renderExternal string
		want           string
	}{
		{
			name: "neither set",
		},
		{
			name:           "render_external_url_derived",
			renderExternal: "https://plimsoll-indexer.onrender.com",
			want:           "https://plimsoll-indexer.onrender.com/healthz",
		},
		{
			name:           "explicit_overrides_render",
			keepAwake:      "https://example.org/ping",
			renderExternal: "https://x.onrender.com",
			want:           "https://example.org/ping",
		},
		{
			name:           "none_disables",
			keepAwake:      "none",
			renderExternal: "https://x.onrender.com",
			want:           "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setBase(t)
			t.Setenv("KEEP_AWAKE_URL", c.keepAwake)
			t.Setenv("RENDER_EXTERNAL_URL", c.renderExternal)
			got, err := Load()
			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}
			if got.KeepAwakeURL != c.want {
				t.Errorf("KeepAwakeURL = %q, want %q", got.KeepAwakeURL, c.want)
			}
		})
	}
}

// TestReportTableListsEveryVariable checks that a valid environment still
// produces a full four-column table.
func TestReportTableListsEveryVariable(t *testing.T) {
	setBase(t)
	_, report := Validate()
	if err := report.Err(); err != nil {
		t.Fatalf("valid environment: %v", err)
	}
	table := report.Table()
	for _, want := range []string{"VARIABLE", "EXPECTED", "VALUE", "ERROR", "DATABASE_URL", "LOG_LEVEL", "RECONCILE_INTERVAL"} {
		if !strings.Contains(table, want) {
			t.Errorf("table missing %q:\n%s", want, table)
		}
	}
	for _, f := range report.Fields {
		if !strings.Contains(table, f.Name) {
			t.Errorf("table missing variable %s:\n%s", f.Name, table)
		}
		if f.Err != nil {
			t.Errorf("valid variable %s has error %v", f.Name, f.Err)
		}
	}
}

// TestReportRedactsSecretValues checks that no secret reaches the table.
func TestReportRedactsSecretValues(t *testing.T) {
	setBase(t)
	secrets := []string{
		"SDI77RAE3QGECMUTGLLQMK47XU4ENI4I364MHOYZJLFLSSOWGOV5BR4B",
		"admin-token-that-must-not-leak",
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"postgres://plimsoll:s3cret-password@localhost/plimsoll",
	}
	t.Setenv("SUPPLY_POSTER_SECRET", secrets[0])
	t.Setenv("WEBHOOK_ADMIN_TOKEN", secrets[1])
	t.Setenv("WEBHOOK_ENCRYPTION_KEY", secrets[2])
	t.Setenv("DATABASE_URL", secrets[3])

	_, report := Validate()
	if err := report.Err(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	table := report.Table()
	for _, secret := range secrets {
		if strings.Contains(table, secret) {
			t.Errorf("table leaks secret %q:\n%s", secret, table)
		}
	}
	if !strings.Contains(table, "<redacted>") {
		t.Errorf("table has no <redacted> marker:\n%s", table)
	}
}

// TestReportCollectsEveryProblem checks that every invalid variable is
// reported in one pass, in both the table and the joined error.
func TestReportCollectsEveryProblem(t *testing.T) {
	setBase(t)
	for _, k := range []string{"DATABASE_URL", "COVERAGE_LEDGER_ID", "REPORTER_REGISTRY_ID"} {
		t.Setenv(k, "")
	}
	t.Setenv("PORT", "nope")
	t.Setenv("INGEST_INTERVAL", "10")
	t.Setenv("LOG_LEVEL", "verbose")
	t.Setenv("CORS_ORIGINS", " ")
	t.Setenv("MAX_FEE_STROOPS", "-1")

	_, report := Validate()
	if report.OK() {
		t.Fatal("Validate() = valid report, want errors")
	}
	table := report.Table()
	want := []string{
		"DATABASE_URL is required",
		"COVERAGE_LEDGER_ID is required",
		"REPORTER_REGISTRY_ID is required",
		"PORT",
		"INGEST_INTERVAL",
		"LOG_LEVEL",
		"CORS_ORIGINS has no origins",
		"MAX_FEE_STROOPS",
	}
	joined := report.Err().Error()
	for _, w := range want {
		if !strings.Contains(table, w) {
			t.Errorf("table missing %q:\n%s", w, table)
		}
		if !strings.Contains(joined, w) {
			t.Errorf("error missing %q: %v", w, report.Err())
		}
	}
}

// TestValidateRejectsBadOptionalValues checks the optional variables are
// type-checked, not just the required ones.
func TestValidateRejectsBadOptionalValues(t *testing.T) {
	cases := []struct {
		name, value, want string
	}{
		{"PORT", "70000", "PORT"},
		{"STELLAR_RPC_URL", "not-a-url", "STELLAR_RPC_URL"},
		{"HORIZON_CHECK_URL", "ftp://example.org", "HORIZON_CHECK_URL"},
		{"KEEP_AWAKE_URL", "localhost:8080", "KEEP_AWAKE_URL"},
		{"LOG_LEVEL", "verbose", "LOG_LEVEL"},
		{"RESOURCE_FEE_MARGIN_PCT", "1.5", "RESOURCE_FEE_MARGIN_PCT"},
		{"CORS_ORIGINS", "app.example.com", "CORS_ORIGINS"},
		{"START_LEDGER", "-1", "START_LEDGER"},
		{"INGEST_INTERVAL", "0s", "INGEST_INTERVAL"},
		{"SUPPLY_POSTER_SECRET", "not-a-seed", "SUPPLY_POSTER_SECRET is not a valid secret seed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setBase(t)
			t.Setenv(c.name, c.value)
			_, report := Validate()
			if err := report.Err(); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("%s=%q: error = %v, want it to contain %q", c.name, c.value, err, c.want)
			}
		})
	}
}

// TestEnvExampleDocumentsEveryVariable keeps .env.example in step with the
// variables the indexer actually reads.
func TestEnvExampleDocumentsEveryVariable(t *testing.T) {
	setBase(t)
	_, report := Validate()
	data, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatalf("read .env.example: %v", err)
	}
	file := string(data)
	for _, f := range report.Fields {
		if !strings.Contains(file, f.Name+"=") {
			t.Errorf(".env.example does not document %s", f.Name)
		}
	}
}

// TestPostingEnabled verifies the PostingEnabled predicate.
func TestPostingEnabled(t *testing.T) {
	setBase(t)
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.PostingEnabled() {
		t.Error("PostingEnabled() = true with no SUPPLY_POSTER_SECRET, want false")
	}

	t.Setenv("SUPPLY_POSTER_SECRET", "SDI77RAE3QGECMUTGLLQMK47XU4ENI4I364MHOYZJLFLSSOWGOV5BR4B")
	got, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !got.PostingEnabled() {
		t.Error("PostingEnabled() = false with SUPPLY_POSTER_SECRET set, want true")
	}
}
