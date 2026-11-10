package client

import (
	"context"
	"echo-service/internal/config"
	"fmt"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
)

type Clients struct{ Database *gorm.DB }

func Open(c config.Database) (*Clients, error) {
	if c.MaxOpenConns != 1 || c.BusyTimeout <= 0 || c.Path == "" {
		return nil, fmt.Errorf("invalid SQLite config")
	}
	path := c.Path
	if path == ":memory:" {
		path = "file:echo-" + uuid.NewString() + "?mode=memory&cache=shared"
	} else {
		var err error
		path, err = filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		if err = f.Close(); err != nil {
			return nil, err
		}
		u := &url.URL{Scheme: "file", Path: path}
		path = u.String()
	}
	separator := "?"
	for _, b := range path {
		if b == '?' {
			separator = "&"
			break
		}
	}
	path += separator + "_pragma=foreign_keys(1)&_pragma=busy_timeout(" + strconv.FormatInt(c.BusyTimeout.Milliseconds(), 10) + ")"
	database, err := gorm.Open(sqlite.Open(path), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	pool, err := database.DB()
	if err != nil {
		return nil, err
	}
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	if err = database.Exec("PRAGMA journal_mode=WAL").Error; err != nil {
		_ = pool.Close()
		return nil, err
	}
	return &Clients{Database: database}, nil
}
func (c *Clients) Ready(ctx context.Context) error {
	if c.Database == nil {
		return fmt.Errorf("database unavailable")
	}
	p, e := c.Database.DB()
	if e != nil {
		return e
	}
	return p.PingContext(ctx)
}
func (c *Clients) Close() error {
	if c.Database == nil {
		return nil
	}
	p, e := c.Database.DB()
	if e != nil {
		return e
	}
	return p.Close()
}
