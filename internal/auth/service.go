package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode"

	"echo-service/internal/config"
	"echo-service/internal/storage"
	"echo-service/internal/table"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var ErrCredentials = errors.New("invalid credentials")
var ErrToken = errors.New("invalid or expired token")
var ErrReplay = errors.New("refresh token reuse detected")

type Principal struct {
	UserID, SessionID, Role, TokenID string
	ExpiresAt                        int64
}
type Tokens struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	TokenType    string `json:"tokenType"`
	ExpiresIn    int64  `json:"expiresIn"`
}

func (s *Service) Active(ctx context.Context, p Principal) bool {
	var n int64
	now := time.Now().UnixMilli()
	err := s.db.WithContext(ctx).Table("auth_sessions a").Joins("JOIN users u ON u.id=a.user_id").Joins("JOIN auth_tokens t ON t.auth_session_id=a.id").Where("a.id=? AND a.user_id=? AND a.revoked_at IS NULL AND a.expires_at>? AND u.status='active' AND t.id=? AND t.kind='access' AND t.expires_at>?", p.SessionID, p.UserID, now, p.TokenID, now).Count(&n).Error
	return err == nil && n == 1 && p.ExpiresAt > now
}

type Service struct {
	db        *gorm.DB
	config    config.Auth
	dummyHash string
}

func New(db *gorm.DB, c config.Auth) *Service {
	dummy, _ := HashPassword("dummy-password-never-used")
	return &Service{db: db, config: c, dummyHash: dummy}
}
func NormalizeUsername(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) < 3 || len(s) > 64 {
		return "", false
	}
	for _, r := range s {
		if r > unicode.MaxASCII || !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r)) {
			return "", false
		}
	}
	return s, true
}
func (s *Service) CreateUser(ctx context.Context, username, display, password, role string) (table.User, error) {
	normalized, ok := NormalizeUsername(username)
	if !ok {
		return table.User{}, errors.New("username must be 3..64 ASCII letters, digits, dot, dash or underscore")
	}
	if role != "admin" && role != "user" {
		return table.User{}, errors.New("invalid platform role")
	}
	if len(display) > 128 {
		return table.User{}, errors.New("displayName too long")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return table.User{}, err
	}
	u := table.User{ID: uuid.NewString(), Username: strings.TrimSpace(username), NormalizedUsername: normalized, DisplayName: display, PasswordHash: hash, PlatformRole: role, Status: "active"}
	err = s.db.WithContext(ctx).Create(&u).Error
	u.PasswordHash = ""
	return u, err
}
func (s *Service) BootstrapAdmin(ctx context.Context, username, password string) (table.User, error) {
	normalized, ok := NormalizeUsername(username)
	if !ok {
		return table.User{}, ErrCredentials
	}
	hash, err := HashPassword(password)
	if err != nil {
		return table.User{}, err
	}
	u := table.User{ID: uuid.NewString(), Username: username, NormalizedUsername: normalized, DisplayName: username, PasswordHash: hash, PlatformRole: "admin", Status: "active"}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&table.User{}).Count(&n).Error; err != nil {
			return err
		}
		if n != 0 {
			return storage.ErrConflict
		}
		return tx.Create(&u).Error
	})
	u.PasswordHash = ""
	return u, err
}
func (s *Service) Login(ctx context.Context, username, password, device string) (Tokens, table.User, error) {
	name, _ := NormalizeUsername(username)
	var u table.User
	err := s.db.WithContext(ctx).First(&u, "normalized_username = ?", name).Error
	hash := u.PasswordHash
	if err != nil {
		hash = s.dummyHash
	}
	valid := VerifyPassword(hash, password)
	if err != nil || !valid || u.Status != "active" {
		return Tokens{}, table.User{}, ErrCredentials
	}
	if len(device) > 128 {
		device = device[:128]
	}
	var result Tokens
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Recheck after password verification so concurrent disable/password changes win.
		var current table.User
		if err := tx.First(&current, "id = ? AND password_hash = ? AND status = ?", u.ID, u.PasswordHash, "active").Error; err != nil {
			return ErrCredentials
		}
		session := table.AuthSession{ID: uuid.NewString(), UserID: u.ID, DeviceName: device, ExpiresAt: time.Now().Add(s.config.RefreshTokenTTL).UnixMilli()}
		if err := tx.Create(&session).Error; err != nil {
			return err
		}
		var err error
		result, err = s.issue(tx, session)
		return err
	})
	u.PasswordHash = ""
	return result, u, err
}
func (s *Service) Authenticate(ctx context.Context, raw string) (Principal, error) {
	if len(raw) < 32 || len(raw) > 512 {
		return Principal{}, ErrToken
	}
	var row struct {
		UserID       string
		SessionID    string
		PlatformRole string
		ExpiresAt    int64
		TokenID      string
	}
	now := time.Now().UnixMilli()
	result := s.db.WithContext(ctx).Table("auth_tokens t").Select("u.id AS user_id,a.id AS session_id,u.platform_role,t.expires_at,t.id AS token_id").Joins("JOIN auth_sessions a ON a.id=t.auth_session_id").Joins("JOIN users u ON u.id=a.user_id").Where("t.token_hash=? AND t.kind='access' AND t.expires_at>? AND a.expires_at>? AND a.revoked_at IS NULL AND u.status='active'", tokenHash(raw), now, now).Scan(&row)
	if result.Error != nil {
		return Principal{}, result.Error
	}
	if row.UserID == "" {
		return Principal{}, ErrToken
	}
	return Principal{UserID: row.UserID, SessionID: row.SessionID, Role: row.PlatformRole, ExpiresAt: row.ExpiresAt, TokenID: row.TokenID}, nil
}
func (s *Service) Refresh(ctx context.Context, raw string) (Tokens, string, error) {
	if len(raw) < 32 || len(raw) > 512 {
		return Tokens{}, "", ErrToken
	}
	var output Tokens
	var userID string
	replay := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var token table.AuthToken
		if err := tx.First(&token, "token_hash = ? AND kind = ?", tokenHash(raw), "refresh").Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrToken
			}
			return err
		}
		var session table.AuthSession
		if err := tx.First(&session, "id = ?", token.AuthSessionID).Error; err != nil {
			return err
		}
		userID = session.UserID
		now := time.Now().UnixMilli()
		if token.UsedAt != nil {
			replay = true
			return tx.Model(&session).Update("revoked_at", now).Error
		}
		if token.ExpiresAt <= now || session.ExpiresAt <= now || session.RevokedAt != nil {
			return ErrToken
		}
		var user table.User
		if err := tx.First(&user, "id = ? AND status = ?", session.UserID, "active").Error; err != nil {
			return ErrToken
		}
		if err := tx.Model(&token).Update("used_at", now).Error; err != nil {
			return err
		}
		// Rotation invalidates prior access tokens of this login session.
		if err := tx.Model(&table.AuthToken{}).Where("auth_session_id = ? AND kind = ?", session.ID, "access").Update("expires_at", now).Error; err != nil {
			return err
		}
		var err error
		output, err = s.issue(tx, session)
		return err
	})
	if err != nil {
		return Tokens{}, userID, err
	}
	if replay {
		return Tokens{}, userID, ErrReplay
	}
	return output, userID, nil
}
func (s *Service) issue(tx *gorm.DB, session table.AuthSession) (Tokens, error) {
	access, e := randomToken()
	if e != nil {
		return Tokens{}, e
	}
	refresh, e := randomToken()
	if e != nil {
		return Tokens{}, e
	}
	now := time.Now()
	expires := now.Add(s.config.AccessTokenTTL).UnixMilli()
	if expires > session.ExpiresAt {
		expires = session.ExpiresAt
	}
	records := []table.AuthToken{{ID: uuid.NewString(), AuthSessionID: session.ID, Kind: "access", TokenHash: tokenHash(access), ExpiresAt: expires}, {ID: uuid.NewString(), AuthSessionID: session.ID, Kind: "refresh", TokenHash: tokenHash(refresh), ExpiresAt: session.ExpiresAt}}
	if e = tx.Create(&records).Error; e != nil {
		return Tokens{}, e
	}
	return Tokens{access, refresh, "Bearer", (expires - now.UnixMilli()) / 1000}, nil
}
func (s *Service) Logout(ctx context.Context, p Principal, all bool) error {
	q := s.db.WithContext(ctx).Model(&table.AuthSession{}).Where("user_id = ?", p.UserID)
	if !all {
		q = q.Where("id = ?", p.SessionID)
	}
	return q.Update("revoked_at", time.Now().UnixMilli()).Error
}
func (s *Service) ChangePassword(ctx context.Context, p Principal, old, new string) error {
	var user table.User
	if err := s.db.WithContext(ctx).First(&user, "id = ?", p.UserID).Error; err != nil {
		return err
	}
	if !VerifyPassword(user.PasswordHash, old) {
		return ErrCredentials
	}
	hash, err := HashPassword(new)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r := tx.Model(&table.User{}).Where("id = ? AND password_hash = ?", user.ID, user.PasswordHash).Update("password_hash", hash)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return ErrCredentials
		}
		return tx.Model(&table.AuthSession{}).Where("user_id = ?", user.ID).Update("revoked_at", time.Now().UnixMilli()).Error
	})
}
func randomToken() (string, error) {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), e
}
func tokenHash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
