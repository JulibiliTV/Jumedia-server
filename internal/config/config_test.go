package config

import "testing"

func TestLoad(t *testing.T) {
	t.Setenv("JUMEDIA_DB_DRIVER", "sqlite")
	t.Setenv("JUMEDIA_ADDR", ":8080")
	t.Setenv("JUMEDIA_DATA_DIR", "data")
	t.Setenv("JUMEDIA_ADMIN_USERNAME", "owner")
	t.Setenv("JUMEDIA_ADMIN_PASSWORD", "secure123")
	c, err := Load()
	if err != nil || c.AdminUsername != "owner" || c.AdminPassword != "secure123" || c.UploadDir() != "data/uploads" || c.Addr != ":8080" {
		t.Fatal(c, err)
	}
	t.Setenv("JUMEDIA_DB_DRIVER", "mysql")
	if _, err = Load(); err == nil {
		t.Fatal("invalid driver")
	}
}
