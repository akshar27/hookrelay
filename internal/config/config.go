// Package config loads all runtime configuration from the environment.
package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config is the fully-resolved runtime configuration. Every field has an env
// tag and a sane default for local development.
type Config struct {
	Env             string        `env:"HOOKRELAY_ENV" envDefault:"development"`
	HTTPAddr        string        `env:"HOOKRELAY_HTTP_ADDR" envDefault:":8080"`
	DatabaseURL     string        `env:"HOOKRELAY_DATABASE_URL" envDefault:"postgres://hookrelay:hookrelay@localhost:5435/hookrelay?sslmode=disable"`
	ShutdownTimeout time.Duration `env:"HOOKRELAY_SHUTDOWN_TIMEOUT" envDefault:"15s"`

	// AdminToken guards every admin/query/dashboard route.
	AdminToken string `env:"HOOKRELAY_ADMIN_TOKEN" envDefault:"dev-admin-token-change-me"`
	// SecretKey (32 bytes, base64) seals endpoint signing secrets at rest. Added in M2.
	SecretKey string `env:"HOOKRELAY_SECRET_KEY" envDefault:""`

	// AllowInsecureEndpoints permits http:// endpoint URLs (local testing only).
	AllowInsecureEndpoints bool `env:"HOOKRELAY_ALLOW_INSECURE_ENDPOINTS" envDefault:"false"`
}

// Load reads the environment into a Config.
func Load() (Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	return c, nil
}

func (c Config) IsProduction() bool { return c.Env == "production" }
