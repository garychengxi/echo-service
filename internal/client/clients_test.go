package client

import (
	"context"
	"echo-service/internal/config"
	"echo-service/internal/storage"
	"testing"
	"time"
)

func TestSQLitePersistenceMigrationsAndConstraints(t *testing.T) {
	cfg := config.Database{Path: t.TempDir() + "/state.sqlite", BusyTimeout: time.Second, MaxOpenConns: 1}
	c, e := Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	if e = storage.Migrate(c.Database); e != nil {
		t.Fatal(e)
	}
	if e = storage.MigrateRuntime(c.Database); e != nil {
		t.Fatal(e)
	}
	if e = storage.Migrate(c.Database); e != nil {
		t.Fatal(e)
	}
	var enabled int
	if e = c.Database.Raw("PRAGMA foreign_keys").Scan(&enabled).Error; e != nil || enabled != 1 {
		t.Fatal("foreign keys disabled", e)
	}
	if c.Database.Exec("INSERT INTO auth_sessions(id,user_id,device_name,expires_at,created_at) VALUES('x','missing','x',1,1)").Error == nil {
		t.Fatal("foreign key not enforced")
	}
	if e = c.Ready(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = c.Close(); e != nil {
		t.Fatal(e)
	}
	c, e = Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	var n int64
	if e = c.Database.Table("schema_migrations").Count(&n).Error; e != nil || n != 2 {
		t.Fatal("schema not persisted", e, n)
	}
}
