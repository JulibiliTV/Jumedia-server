// Package config defines the environment-only server configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Addr, DBDriver, DBDSN, DataDir, StaticDir, WebDir, PublicOrigin string
	AdminUsername, AdminPassword                                    string
}

func Load() (Config, error) {
	env := func(k, d string) string {
		if v, ok := os.LookupEnv(k); ok {
			return v
		}
		return d
	}
	c := Config{Addr: env("JUMEDIA_ADDR", ":8080"), DBDriver: env("JUMEDIA_DB_DRIVER", "sqlite"), DBDSN: env("JUMEDIA_DB_DSN", "data/jumedia.db"), DataDir: env("JUMEDIA_DATA_DIR", "data"), StaticDir: env("JUMEDIA_STATIC_DIR", "../Jumedia-web/public"), WebDir: env("JUMEDIA_WEB_DIR", ""), PublicOrigin: strings.TrimRight(env("JUMEDIA_PUBLIC_ORIGIN", ""), "/")}
	c.AdminUsername = env("JUMEDIA_ADMIN_USERNAME", "")
	c.AdminPassword = env("JUMEDIA_ADMIN_PASSWORD", "")
	if c.DBDriver != "sqlite" && c.DBDriver != "postgres" {
		return c, fmt.Errorf("unsupported JUMEDIA_DB_DRIVER %q", c.DBDriver)
	}
	if c.Addr == "" || c.DBDSN == "" || c.DataDir == "" {
		return c, fmt.Errorf("address, database DSN and data directory must not be empty")
	}
	return c, nil
}
func (c Config) UploadDir() string { return filepath.Join(c.DataDir, "uploads") }
