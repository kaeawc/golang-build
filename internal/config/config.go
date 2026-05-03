// Package config loads runtime configuration from the environment.
//
// MigrationDatabaseURL must be a direct (non-pooled) connection: Neon's
// pooler returns NULL for CURRENT_SCHEMA(), which breaks golang-migrate.
package config

import (
	"fmt"

	"github.com/kaeawc/golang-build/internal/env"
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
	}, nil
}
