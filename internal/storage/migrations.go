// Package storage owns schema versions and repositories, independent of HTTP.
package storage

import (
	"fmt"
	"gorm.io/gorm"
)

// Migrate applies reviewed SQL in one transaction. It never calls AutoMigrate.
func Migrate(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Raw(`SELECT count(*) FROM schema_migrations WHERE version=1`).Scan(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return nil
		}
		statements := []string{
			`CREATE TABLE users(id TEXT PRIMARY KEY, username TEXT NOT NULL, normalized_username TEXT NOT NULL UNIQUE, display_name TEXT NOT NULL, password_hash TEXT NOT NULL, platform_role TEXT NOT NULL CHECK(platform_role IN ('admin','user')), status TEXT NOT NULL CHECK(status IN ('active','disabled')), created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
			`CREATE TABLE auth_sessions(id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id),device_name TEXT NOT NULL,expires_at INTEGER NOT NULL,revoked_at INTEGER,created_at INTEGER NOT NULL)`,
			`CREATE INDEX auth_sessions_user ON auth_sessions(user_id,revoked_at)`,
			`CREATE TABLE auth_tokens(id TEXT PRIMARY KEY,auth_session_id TEXT NOT NULL REFERENCES auth_sessions(id),kind TEXT NOT NULL CHECK(kind IN ('access','refresh')),token_hash TEXT NOT NULL UNIQUE,expires_at INTEGER NOT NULL,used_at INTEGER,created_at INTEGER NOT NULL)`,
			`CREATE INDEX auth_tokens_session ON auth_tokens(auth_session_id)`,
			`CREATE TABLE containers(id TEXT PRIMARY KEY,name TEXT NOT NULL,endpoint TEXT NOT NULL UNIQUE,status TEXT NOT NULL CHECK(status IN ('enabled','disabled')),workspace_roots_json TEXT NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL)`,
			`CREATE TABLE user_containers(id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id),container_id TEXT NOT NULL REFERENCES containers(id),role TEXT NOT NULL CHECK(role='owner'),status TEXT NOT NULL CHECK(status IN ('active','revoked')),assigned_at INTEGER NOT NULL,revoked_at INTEGER,UNIQUE(user_id,container_id))`,
			`CREATE UNIQUE INDEX single_active_container_owner ON user_containers(container_id) WHERE status='active'`,
			`CREATE INDEX assignments_user ON user_containers(user_id,status)`,
			`CREATE TABLE audit_events(id TEXT PRIMARY KEY,actor_user_id TEXT NOT NULL,action TEXT NOT NULL,resource_id TEXT NOT NULL,outcome TEXT NOT NULL,created_at INTEGER NOT NULL)`,
			`CREATE INDEX audit_events_created ON audit_events(created_at)`,
			`INSERT INTO schema_migrations(version,applied_at) VALUES(1,CAST(strftime('%s','now') AS INTEGER)*1000)`,
		}
		for _, q := range statements {
			if err := tx.Exec(q).Error; err != nil {
				return fmt.Errorf("migration 1 failed: %w", err)
			}
		}
		return nil
	})
}
