package config

import (
	"strings"
	"testing"

	"github.com/kaeawc/golang-build/internal/env"
)

// FuzzLoad drives Load with arbitrary environment values. It pins down
// exactly when Load may fail (missing DATABASE_URL or an unknown
// ENVIRONMENT) and what every successful Config looks like: required
// fields filled, defaults applied, and no stray whitespace.
func FuzzLoad(f *testing.F) {
	f.Add("postgres://localhost/app", "", "", "")
	f.Add(" postgres://pooler/app ", "postgres://direct/app", "9090", "production")
	f.Add("x", "", "\t", "staging")
	f.Add("", "postgres://direct/app", "8080", "preview")
	f.Add(" ", "", "", " development\n")
	f.Fuzz(func(t *testing.T, dbURL, directURL, port, envName string) {
		r := env.Map{
			"DATABASE_URL":        dbURL,
			"DATABASE_URL_DIRECT": directURL,
			"PORT":                port,
			"ENVIRONMENT":         envName,
		}
		cfg, err := Load(r)

		wantEnv := Environment(strings.TrimSpace(envName))
		if wantEnv == "" {
			wantEnv = EnvDevelopment
		}
		wantErr := strings.TrimSpace(dbURL) == "" || !wantEnv.Valid()
		if wantErr != (err != nil) {
			t.Fatalf("Load(%q) error = %v, want error: %t", r, err, wantErr)
		}
		if err != nil {
			return
		}

		if cfg.Environment != wantEnv {
			t.Fatalf("Environment = %q, want %q", cfg.Environment, wantEnv)
		}
		if cfg.DatabaseURL != strings.TrimSpace(dbURL) {
			t.Fatalf("DatabaseURL = %q, want trimmed %q", cfg.DatabaseURL, dbURL)
		}
		if strings.TrimSpace(directURL) == "" && cfg.MigrationDatabaseURL != cfg.DatabaseURL {
			t.Fatalf("MigrationDatabaseURL = %q, want fallback to DatabaseURL %q", cfg.MigrationDatabaseURL, cfg.DatabaseURL)
		}
		for name, v := range map[string]string{
			"Port":                 cfg.Port,
			"MigrationDatabaseURL": cfg.MigrationDatabaseURL,
		} {
			if v == "" || v != strings.TrimSpace(v) {
				t.Fatalf("%s = %q, want non-empty and trimmed", name, v)
			}
		}
	})
}
