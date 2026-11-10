package storage

import (
	"context"
	"echo-service/internal/table"
	"errors"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"time"
)

var ErrNotFound = errors.New("resource not found")
var ErrConflict = errors.New("resource conflict")

// Directory is the persistence port. A PostgreSQL implementation can replace SQL
// without changing the cache decorator or application services.
type Directory interface {
	User(context.Context, string) (table.User, error)
	Users(context.Context, int, int) ([]table.User, error)
	CreateUser(context.Context, *table.User) error
	UpdateUser(context.Context, string, map[string]any) error
	Container(context.Context, string) (table.Container, error)
	Containers(context.Context, int, int) ([]table.Container, error)
	CreateContainer(context.Context, *table.Container) error
	UpdateContainer(context.Context, string, map[string]any) error
}
type SQL struct{ DB *gorm.DB }

func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}
func (s *SQL) User(ctx context.Context, id string) (u table.User, err error) {
	err = s.DB.WithContext(ctx).First(&u, "id = ?", id).Error
	return u, notFound(err)
}
func (s *SQL) Users(ctx context.Context, limit, offset int) (u []table.User, err error) {
	err = s.DB.WithContext(ctx).Order("created_at,id").Limit(limit).Offset(offset).Find(&u).Error
	return
}
func (s *SQL) CreateUser(ctx context.Context, u *table.User) error {
	return s.DB.WithContext(ctx).Create(u).Error
}
func (s *SQL) UpdateUser(ctx context.Context, id string, values map[string]any) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var u table.User
		if err := tx.First(&u, "id = ?", id).Error; err != nil {
			return notFound(err)
		}
		if u.PlatformRole == "admin" && u.Status == "active" && (values["status"] == "disabled" || values["platform_role"] == "user") {
			var n int64
			if err := tx.Model(&table.User{}).Where("platform_role = ? AND status = ?", "admin", "active").Count(&n).Error; err != nil {
				return err
			}
			if n <= 1 {
				return ErrConflict
			}
		}
		if err := tx.Model(&u).Updates(values).Error; err != nil {
			return err
		}
		// User security changes invalidate sessions in the same transaction.
		if _, ok := values["status"]; ok {
			return tx.Model(&table.AuthSession{}).Where("user_id = ? AND revoked_at IS NULL", id).Update("revoked_at", time.Now().UnixMilli()).Error
		}
		if _, ok := values["platform_role"]; ok {
			return tx.Model(&table.AuthSession{}).Where("user_id = ? AND revoked_at IS NULL", id).Update("revoked_at", time.Now().UnixMilli()).Error
		}
		return nil
	})
}
func (s *SQL) Container(ctx context.Context, id string) (c table.Container, err error) {
	err = s.DB.WithContext(ctx).First(&c, "id = ?", id).Error
	return c, notFound(err)
}
func (s *SQL) Containers(ctx context.Context, limit, offset int) (c []table.Container, err error) {
	err = s.DB.WithContext(ctx).Order("created_at,id").Limit(limit).Offset(offset).Find(&c).Error
	return
}
func (s *SQL) CreateContainer(ctx context.Context, c *table.Container) error {
	return s.DB.WithContext(ctx).Create(c).Error
}
func (s *SQL) UpdateContainer(ctx context.Context, id string, values map[string]any) error {
	r := s.DB.WithContext(ctx).Model(&table.Container{}).Where("id = ?", id).Updates(values)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQL) Assignment(ctx context.Context, user, container string) (a table.UserContainer, err error) {
	err = s.DB.WithContext(ctx).First(&a, "user_id = ? AND container_id = ? AND status = ?", user, container, "active").Error
	return a, notFound(err)
}
func (s *SQL) Assign(ctx context.Context, user, container string) (table.UserContainer, error) {
	a := table.UserContainer{ID: uuid.NewString(), UserID: user, ContainerID: container, Role: "owner", Status: "active", AssignedAt: time.Now().UnixMilli()}
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var u table.User
		var c table.Container
		if err := tx.First(&u, "id = ? AND status = ?", user, "active").Error; err != nil {
			return notFound(err)
		}
		if err := tx.First(&c, "id = ? AND status = ?", container, "enabled").Error; err != nil {
			return notFound(err)
		}
		var current table.UserContainer
		err := tx.First(&current, "container_id = ? AND status = ?", container, "active").Error
		if err == nil {
			if current.UserID != user {
				return ErrConflict
			}
			a = current
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var prior int64
		if err := tx.Model(&table.UserContainer{}).Where("container_id = ?", container).Count(&prior).Error; err != nil {
			return err
		}
		// Reassignment requires an explicit future reset workflow; do not leak old workspaces.
		if prior > 0 {
			return ErrConflict
		}
		return tx.Create(&a).Error
	})
	return a, err
}
func (s *SQL) RevokeAssignment(ctx context.Context, user, container string) error {
	return s.DB.WithContext(ctx).Model(&table.UserContainer{}).Where("user_id = ? AND container_id = ? AND status = ?", user, container, "active").Updates(map[string]any{"status": "revoked", "revoked_at": time.Now().UnixMilli()}).Error
}
func (s *SQL) Assignments(ctx context.Context, container string) (a []table.UserContainer, err error) {
	err = s.DB.WithContext(ctx).Where("container_id = ?", container).Order("assigned_at,id").Find(&a).Error
	return
}
func (s *SQL) UserContainers(ctx context.Context, user string) (c []table.Container, err error) {
	err = s.DB.WithContext(ctx).Table("containers").Select("containers.*").Joins("JOIN user_containers a ON a.container_id = containers.id").Where("a.user_id = ? AND a.status = ? AND containers.status = ?", user, "active", "enabled").Order("containers.created_at,containers.id").Scan(&c).Error
	return
}
func (s *SQL) Audit(ctx context.Context, actor, action, resource, outcome string) error {
	return s.DB.WithContext(ctx).Create(&table.AuditEvent{ID: uuid.NewString(), ActorUserID: actor, Action: action, ResourceID: resource, Outcome: outcome}).Error
}
