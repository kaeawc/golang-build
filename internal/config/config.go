// Package config loads runtime configuration from the environment.
//
// MigrationDatabaseURL must be a direct (non-pooled) connection: Neon's
// pooler returns NULL for CURRENT_SCHEMA(), which breaks golang-migrate.
package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strings"

	"github.com/kaeawc/golang-build/internal/env"
	"github.com/kaeawc/golang-build/internal/logger"
)

type Environment string

const (
	EnvDevelopment Environment = "development"
	EnvPreview     Environment = "preview"
	EnvProduction  Environment = "production"
)

func (e Environment) Valid() bool {
	switch e {
	case EnvDevelopment, EnvPreview, EnvProduction:
		return true
	}
	return false
}

// Fly captures the Fly.io machine identity. All fields are empty when not
// running on Fly. Populated from FLY_APP_NAME, FLY_REGION, FLY_MACHINE_ID,
// FLY_PUBLIC_IP — set by the Fly runtime on every machine.
type Fly struct {
	AppName   string
	Region    string
	MachineID string
	PublicIP  string
}

func (f Fly) OnFly() bool { return f.AppName != "" }

type Config struct {
	Port                 string
	Environment          Environment
	DatabaseURL          string
	MigrationDatabaseURL string
	ValkeyAddr           string
	Fly                  Fly
	// LogLevel comes from LOG_LEVEL (debug, info, warn, error; default info).
	LogLevel slog.Level
	// LogFormat comes from LOG_FORMAT (json or text). Defaults to text in
	// development and JSON everywhere else.
	LogFormat logger.Format
	// DebugAddr comes from DEBUG_ADDR: the listen address of the pprof
	// debug server (e.g. 127.0.0.1:6060). Empty disables it. The host
	// must be loopback unless DEBUG_ALLOW_NON_LOOPBACK is true.
	DebugAddr string
}

func Load(r env.Reader) (*Config, error) {
	dbURL := env.GetTrim(r, "DATABASE_URL")
	if dbURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	migrateURL := env.GetTrim(r, "DATABASE_URL_DIRECT")
	if migrateURL == "" {
		migrateURL = dbURL
	}
	port := env.GetTrim(r, "PORT")
	if port == "" {
		port = "8080"
	}
	envName := Environment(env.GetTrim(r, "ENVIRONMENT"))
	if envName == "" {
		envName = EnvDevelopment
	}
	if !envName.Valid() {
		return nil, fmt.Errorf("ENVIRONMENT %q: must be one of %s, %s, %s", envName, EnvDevelopment, EnvPreview, EnvProduction)
	}
	level, format, err := loadLogging(r, envName)
	if err != nil {
		return nil, err
	}
	debugAddr, err := loadDebugAddr(r)
	if err != nil {
		return nil, err
	}
	return &Config{
		Port:                 port,
		Environment:          envName,
		DatabaseURL:          dbURL,
		MigrationDatabaseURL: migrateURL,
		ValkeyAddr:           env.GetTrim(r, "VALKEY_ADDR"),
		Fly: Fly{
			AppName:   env.GetTrim(r, "FLY_APP_NAME"),
			Region:    env.GetTrim(r, "FLY_REGION"),
			MachineID: env.GetTrim(r, "FLY_MACHINE_ID"),
			PublicIP:  env.GetTrim(r, "FLY_PUBLIC_IP"),
		},
		LogLevel:  level,
		LogFormat: format,
		DebugAddr: debugAddr,
	}, nil
}

// loadDebugAddr reads DEBUG_ADDR. pprof exposes heap contents, command
// lines and CPU-burning endpoints, so a host that is not loopback
// (including an empty host, which binds every interface) is refused
// unless DEBUG_ALLOW_NON_LOOPBACK opts in.
func loadDebugAddr(r env.Reader) (string, error) {
	addr := env.GetTrim(r, "DEBUG_ADDR")
	if addr == "" {
		return "", nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("DEBUG_ADDR %q: %w", addr, err)
	}
	allow, err := env.GetBool(r, "DEBUG_ALLOW_NON_LOOPBACK", false)
	if err != nil {
		return "", err
	}
	if !allow && !isLoopbackHost(host) {
		return "", fmt.Errorf("DEBUG_ADDR %q: host must be loopback (127.0.0.1, ::1 or localhost); set DEBUG_ALLOW_NON_LOOPBACK=true to expose pprof beyond this machine", addr)
	}
	return addr, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

func loadLogging(r env.Reader, envName Environment) (slog.Level, logger.Format, error) {
	level := slog.LevelInfo
	if v := env.GetTrim(r, "LOG_LEVEL"); v != "" {
		if err := level.UnmarshalText([]byte(v)); err != nil {
			return 0, 0, fmt.Errorf("LOG_LEVEL %q: must be debug, info, warn or error", v)
		}
	}
	format := logger.FormatJSON
	if envName == EnvDevelopment {
		format = logger.FormatText
	}
	if v := env.GetTrim(r, "LOG_FORMAT"); v != "" {
		f, err := logger.ParseFormat(v)
		if err != nil {
			return 0, 0, fmt.Errorf("LOG_FORMAT: %w", err)
		}
		format = f
	}
	return level, format, nil
}
