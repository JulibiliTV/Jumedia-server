package config

import (
	"os"
	"testing"
)

func TestLoad(t *testing.T) {
	for _, p := range [][2]string{{"JUMEDIA_ADDR", ":8080"}, {"JUMEDIA_DB_DRIVER", "sqlite"}, {"JUMEDIA_DB_DSN", "data/jumedia.db"}, {"JUMEDIA_DATA_DIR", "data"}, {"JUMEDIA_STATIC_DIR", "../Jumedia-web/public"}, {"JUMEDIA_WEB_DIR", ""}, {"JUMEDIA_PUBLIC_ORIGIN", ""}, {"JUMEDIA_SEED", "true"}} {
		t.Setenv(p[0], p[1])
	}
	c, err := Load()
	if err != nil || !c.Seed || c.UploadDir() != "data/uploads" || c.Addr != ":8080" {
		t.Fatalf("%+v %v", c, err)
	}
	t.Setenv("JUMEDIA_SEED", "nope")
	if _, err = Load(); err == nil {
		t.Fatal("invalid bool")
	}
	t.Setenv("JUMEDIA_SEED", "false")
	t.Setenv("JUMEDIA_DB_DRIVER", "mysql")
	if _, err = Load(); err == nil {
		t.Fatal("invalid driver")
	}
}

func TestEmbyConfiguration(t *testing.T) {
	t.Setenv("JUMEDIA_STATIC_DIR", "/srv/static")
	t.Setenv("JUMEDIA_EMBY_URL", "https://emby.example.test/")
	t.Setenv("JUMEDIA_EMBY_USERNAME", "operator")
	t.Setenv("JUMEDIA_EMBY_PASSWORD", "")
	t.Setenv("JUMEDIA_EMBY_CATALOG", "")
	if err := os.Unsetenv("JUMEDIA_EMBY_CATALOG"); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil || c.EmbyURL != "https://emby.example.test/emby" || c.EmbyUsername != "operator" || c.EmbyPassword != "" || c.EmbyCatalog != "/srv/static/mock/emby/catalog.json" {
		t.Fatal(c, err)
	}
	t.Setenv("JUMEDIA_EMBY_URL", "https://emby.example.test/emby/")
	c, err = Load()
	if err != nil || c.EmbyURL != "https://emby.example.test/emby" {
		t.Fatal(c, err)
	}
	t.Setenv("JUMEDIA_EMBY_USERNAME", "")
	c, err = Load()
	if err != nil || c.EmbyUsername != "" {
		t.Fatal("username must have no code default")
	}
}
