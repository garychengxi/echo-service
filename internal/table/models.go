package table

// Persistence records are internal: JSON DTOs must be explicitly projected.
type User struct {
	ID                 string `gorm:"primaryKey"`
	Username           string
	NormalizedUsername string `gorm:"uniqueIndex"`
	DisplayName        string
	PasswordHash       string
	PlatformRole       string
	Status             string
	CreatedAt          int64 `gorm:"autoCreateTime:milli"`
	UpdatedAt          int64 `gorm:"autoUpdateTime:milli"`
}
type AuthSession struct {
	ID         string `gorm:"primaryKey"`
	UserID     string `gorm:"index"`
	DeviceName string
	ExpiresAt  int64
	RevokedAt  *int64
	CreatedAt  int64 `gorm:"autoCreateTime:milli"`
}
type AuthToken struct {
	ID            string `gorm:"primaryKey"`
	AuthSessionID string `gorm:"index"`
	Kind          string
	TokenHash     string `gorm:"uniqueIndex"`
	ExpiresAt     int64
	UsedAt        *int64
	CreatedAt     int64 `gorm:"autoCreateTime:milli"`
}
type Container struct {
	ID                 string `gorm:"primaryKey"`
	Name               string
	Endpoint           string `gorm:"uniqueIndex"`
	WebURL             string
	Status             string
	WorkspaceRootsJSON string
	CreatedAt          int64 `gorm:"autoCreateTime:milli"`
	UpdatedAt          int64 `gorm:"autoUpdateTime:milli"`
}
type UserContainer struct {
	ID          string `gorm:"primaryKey"`
	UserID      string `gorm:"uniqueIndex:assignment_user_container"`
	ContainerID string `gorm:"uniqueIndex:assignment_user_container;index"`
	Role        string
	Status      string
	AssignedAt  int64
	RevokedAt   *int64
}
type AuditEvent struct {
	ID          string `gorm:"primaryKey"`
	ActorUserID string `gorm:"index"`
	Action      string
	ResourceID  string
	Outcome     string
	CreatedAt   int64 `gorm:"autoCreateTime:milli;index"`
}
