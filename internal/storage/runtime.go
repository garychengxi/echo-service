package storage

import (
	"encoding/json"
	"gorm.io/gorm"
	"strings"
)

type Project struct {
	ID           string `gorm:"primaryKey" json:"id"`
	UserID       string `json:"-"`
	AssignmentID string `json:"-"`
	ContainerID  string `json:"containerId"`
	RemoteID     string `json:"-"`
	Name         string `json:"name"`
	RootsJSON    string `json:"-"`
	CreatedAt    int64  `gorm:"autoCreateTime:milli" json:"createdAt"`
}
type Session struct {
	ID           string  `gorm:"primaryKey" json:"id"`
	UserID       string  `json:"-"`
	AssignmentID string  `json:"-"`
	ContainerID  string  `json:"containerId"`
	RemoteID     string  `json:"-"`
	ProjectID    *string `json:"projectId"`
	Title        string  `json:"title"`
	Cwd          string  `json:"cwd"`
	ModelID      string  `json:"modelId"`
	ModeID       string  `json:"modeId"`
	CreatedAt    int64   `gorm:"autoCreateTime:milli" json:"createdAt"`
}

// Service sessions always execute in an assigned remote container.
func (s Session) MarshalJSON() ([]byte, error) {
	type alias Session
	return json.Marshal(struct {
		alias
		ExecutionType string `json:"executionType"`
	}{alias(s), "remote"})
}

type Turn struct {
	ID              string `gorm:"primaryKey" json:"id"`
	SessionID       string `json:"sessionId"`
	UserID          string `json:"-"`
	ClientRequestID string `json:"clientRequestId"`
	PayloadHash     string `json:"-"`
	State           string `json:"state"`
	StopReason      string `json:"stopReason,omitempty"`
	ErrorMessage    string `json:"errorMessage,omitempty"`
	CreatedAt       int64  `gorm:"autoCreateTime:milli" json:"createdAt"`
	FinishedAt      *int64 `json:"finishedAt,omitempty"`
}

// Operation reserves a non-idempotent remote mutation before any bytes are sent.
// An interrupted operation is deliberately not retried automatically.
type Operation struct {
	ID          string `gorm:"primaryKey"`
	UserID      string
	ContainerID string
	Kind        string
	Fingerprint string
	ResourceID  string
	CreatedAt   int64 `gorm:"autoCreateTime:milli"`
}

func MigrateOperations(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`CREATE TABLE IF NOT EXISTS operations(id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id),container_id TEXT NOT NULL REFERENCES containers(id),kind TEXT NOT NULL,fingerprint TEXT NOT NULL,resource_id TEXT NOT NULL,created_at INTEGER NOT NULL)`).Error; err != nil {
			return err
		}
		return tx.Exec(`INSERT OR IGNORE INTO schema_migrations(version,applied_at) VALUES(3,CAST(strftime('%s','now') AS INTEGER)*1000)`).Error
	})
}

func MigrateRuntime(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Raw("SELECT count(*) FROM schema_migrations WHERE version=2").Scan(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			for _, sql := range []string{
				`CREATE TABLE projects(id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id),assignment_id TEXT NOT NULL REFERENCES user_containers(id),container_id TEXT NOT NULL REFERENCES containers(id),remote_id TEXT NOT NULL,name TEXT NOT NULL,roots_json TEXT NOT NULL,created_at INTEGER NOT NULL,UNIQUE(container_id,remote_id))`,
				`CREATE INDEX projects_owner ON projects(user_id,container_id,created_at,id)`,
				`CREATE TABLE sessions(id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id),assignment_id TEXT NOT NULL REFERENCES user_containers(id),container_id TEXT NOT NULL REFERENCES containers(id),remote_id TEXT NOT NULL,project_id TEXT REFERENCES projects(id) ON DELETE SET NULL,title TEXT NOT NULL,cwd TEXT NOT NULL,model_id TEXT NOT NULL,created_at INTEGER NOT NULL,UNIQUE(container_id,remote_id))`,
				`CREATE INDEX sessions_owner ON sessions(user_id,container_id,project_id,created_at,id)`,
				`CREATE TABLE turns(id TEXT PRIMARY KEY,session_id TEXT NOT NULL REFERENCES sessions(id),user_id TEXT NOT NULL REFERENCES users(id),client_request_id TEXT NOT NULL,payload_hash TEXT NOT NULL,state TEXT NOT NULL,stop_reason TEXT NOT NULL,created_at INTEGER NOT NULL,finished_at INTEGER,UNIQUE(session_id,client_request_id))`,
				`CREATE UNIQUE INDEX single_active_turn ON turns(session_id) WHERE state IN ('running','awaiting_approval','cancelling')`,
				`INSERT INTO schema_migrations(version,applied_at) VALUES(2,CAST(strftime('%s','now') AS INTEGER)*1000)`,
			} {
				if err := tx.Exec(sql).Error; err != nil {
					return err
				}
			}
		}
		if err := tx.Exec("ALTER TABLE containers ADD COLUMN web_url TEXT NOT NULL DEFAULT ''").Error; err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
		if err := tx.Exec("ALTER TABLE sessions ADD COLUMN mode_id TEXT NOT NULL DEFAULT 'always-ask'").Error; err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
		if err := tx.Exec("ALTER TABLE turns ADD COLUMN error_message TEXT NOT NULL DEFAULT ''").Error; err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
		return nil
	})
}
