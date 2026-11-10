// Package table contains persistence models and repositories.
// Schema changes belong in deploy/scripts; no automatic startup migrations run.
package table

import "time"

type Base struct {
	ID        string    `gorm:"type:varchar(64);primaryKey" json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
