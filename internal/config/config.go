// Package config defines the environment-only server configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Addr, DBDriver, DBDSN, DataDir, StaticDir, WebDir, PublicOrigin string
	EmbyURL, EmbyUsername, EmbyPassword, EmbyCatalog                string
	Seed                                                            bool
}

func Load() (Config, error) {
	env := func(k, d string) string {
		if v, ok := os.LookupEnv(k); ok {
			return v
		}
		return d
	}
	c := Config{Addr: env("JUMEDIA_ADDR", ":8080"), DBDriver: env("JUMEDIA_DB_DRIVER", "sqlite"), DBDSN: env("JUMEDIA_DB_DSN", "data/jumedia.db"), DataDir: env("JUMEDIA_DATA_DIR", "data"), StaticDir: env("JUMEDIA_STATIC_DIR", "../Jumedia-web/public"), WebDir: env("JUMEDIA_WEB_DIR", ""), PublicOrigin: strings.TrimRight(env("JUMEDIA_PUBLIC_ORIGIN", ""), "/")}
	c.EmbyURL = strings.TrimRight(env("JUMEDIA_EMBY_URL", ""), "/")
	if c.EmbyURL != "" && !strings.HasSuffix(c.EmbyURL, "/emby") {
		c.EmbyURL += "/emby"
	}
	c.EmbyUsername = env("JUMEDIA_EMBY_USERNAME", "")
	c.EmbyPassword = env("JUMEDIA_EMBY_PASSWORD", "")
	c.EmbyCatalog = env("JUMEDIA_EMBY_CATALOG", filepath.Join(c.StaticDir, "mock", "emby", "catalog.json"))
	var err error
	c.Seed, err = strconv.ParseBool(env("JUMEDIA_SEED", "true"))
	if err != nil {
		return c, fmt.Errorf("JUMEDIA_SEED: %w", err)
	}
	if c.DBDriver != "sqlite" && c.DBDriver != "postgres" {
		return c, fmt.Errorf("unsupported JUMEDIA_DB_DRIVER %q", c.DBDriver)
	}
	if c.Addr == "" || c.DBDSN == "" || c.DataDir == "" {
		return c, fmt.Errorf("address, database DSN and data directory must not be empty")
	}
	return c, nil
}
func (c Config) UploadDir() string { return filepath.Join(c.DataDir, "uploads") }
