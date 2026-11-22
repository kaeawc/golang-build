package config

import (
	"log/slog"
	"testing"

	"github.com/kaeawc/golang-build/internal/env"
	"github.com/kaeawc/golang-build/internal/logger"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env.Map{"DATABASE_URL": "postgres://db"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != "8080" || cfg.Environment != EnvDevelopment || cfg.MigrationDatabaseURL != "postgres://db" {
		t.Errorf("defaults: %+v", cfg)
	}
	if cfg.LogLevel != slog.LevelInfo || cfg.LogFormat != logger.FormatText {
		t.Errorf("development logging = %v/%v, want info/text", cfg.LogLevel, cfg.LogFormat)
	}
}

func TestLoadLogging(t *testing.T) {
	tests := []struct {
		name   string
		vars   env.Map
		level  slog.Level
		format logger.Format
	}{
		{"production defaults to json", env.Map{"ENVIRONMENT": "production"}, slog.LevelInfo, logger.FormatJSON},
		{"preview defaults to json", env.Map{"ENVIRONMENT": "preview"}, slog.LevelInfo, logger.FormatJSON},
		{"explicit level and format", env.Map{"ENVIRONMENT": "production", "LOG_LEVEL": "debug", "LOG_FORMAT": "text"}, slog.LevelDebug, logger.FormatText},
		{"level is case-insensitive", env.Map{"LOG_LEVEL": "WARN"}, slog.LevelWarn, logger.FormatText},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.vars["DATABASE_URL"] = "postgres://db"
			cfg, err := Load(tt.vars)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.LogLevel != tt.level || cfg.LogFormat != tt.format {
				t.Errorf("got %v/%v, want %v/%v", cfg.LogLevel, cfg.LogFormat, tt.level, tt.format)
			}
		})
	}
}

func TestLoadErrors(t *testing.T) {
	for name, vars := range map[string]env.Map{
		"missing DATABASE_URL": {},
		"bad ENVIRONMENT":      {"DATABASE_URL": "x", "ENVIRONMENT": "staging"},
		"bad LOG_LEVEL":        {"DATABASE_URL": "x", "LOG_LEVEL": "loud"},
		"bad LOG_FORMAT":       {"DATABASE_URL": "x", "LOG_FORMAT": "yaml"},
	} {
		if _, err := Load(vars); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestLoadDebugAddr(t *testing.T) {
	tests := []struct {
		name string
		vars env.Map
		want string
	}{
		{"unset disables", env.Map{}, ""},
		{"ipv4 loopback", env.Map{"DEBUG_ADDR": "127.0.0.1:6060"}, "127.0.0.1:6060"},
		{"other 127/8 address", env.Map{"DEBUG_ADDR": "127.0.0.2:6060"}, "127.0.0.2:6060"},
		{"ipv6 loopback", env.Map{"DEBUG_ADDR": "[::1]:6060"}, "[::1]:6060"},
		{"localhost", env.Map{"DEBUG_ADDR": "localhost:6060"}, "localhost:6060"},
		{"trimmed", env.Map{"DEBUG_ADDR": " 127.0.0.1:6060 "}, "127.0.0.1:6060"},
		{"non-loopback allowed explicitly", env.Map{"DEBUG_ADDR": "0.0.0.0:6060", "DEBUG_ALLOW_NON_LOOPBACK": "true"}, "0.0.0.0:6060"},
		{"all interfaces allowed explicitly", env.Map{"DEBUG_ADDR": ":6060", "DEBUG_ALLOW_NON_LOOPBACK": "1"}, ":6060"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.vars["DATABASE_URL"] = "postgres://db"
			cfg, err := Load(tt.vars)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.DebugAddr != tt.want {
				t.Errorf("DebugAddr = %q, want %q", cfg.DebugAddr, tt.want)
			}
		})
	}
}

func TestLoadDebugAddrRefusesNonLoopback(t *testing.T) {
	for name, vars := range map[string]env.Map{
		"all interfaces":       {"DEBUG_ADDR": ":6060"},
		"unspecified ipv4":     {"DEBUG_ADDR": "0.0.0.0:6060"},
		"unspecified ipv6":     {"DEBUG_ADDR": "[::]:6060"},
		"private address":      {"DEBUG_ADDR": "10.0.0.5:6060"},
		"hostname":             {"DEBUG_ADDR": "debug.internal:6060"},
		"opt-out set to false": {"DEBUG_ADDR": "0.0.0.0:6060", "DEBUG_ALLOW_NON_LOOPBACK": "false"},
		"missing port":         {"DEBUG_ADDR": "127.0.0.1"},
		"bad opt-in value":     {"DEBUG_ADDR": "0.0.0.0:6060", "DEBUG_ALLOW_NON_LOOPBACK": "maybe"},
	} {
		vars["DATABASE_URL"] = "postgres://db"
		if cfg, err := Load(vars); err == nil {
			t.Errorf("%s: DebugAddr = %q, want error", name, cfg.DebugAddr)
		}
	}
}
