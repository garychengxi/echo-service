package config

import (
	"code.cestc.cn/ai-socket/server-go/conf"
	"fmt"
	"github.com/spf13/viper"
	"net"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Server          conf.Config
	Database        Database
	Cache           Cache
	Auth            Auth
	AgentConnection AgentConnection
	EventStream     EventStream
	ShutdownTimeout time.Duration
}
type Database struct {
	Path         string
	BusyTimeout  time.Duration
	MaxOpenConns int
}
type Cache struct {
	Provider        string
	TTL             time.Duration
	CleanupInterval time.Duration
}
type Auth struct {
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	Issuer          string
	SingleSession   bool
}
type AgentConnection struct {
	DialTimeout     time.Duration
	RequestTimeout  time.Duration
	WriteQueueSize  int
	MaxMessageBytes int64
}
type EventStream struct {
	SubscriberQueueSize int
}

func Load(v *viper.Viper) (*Config, error) {
	v.SetEnvPrefix("ECHO")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	for _, key := range []string{"HTTPServer.Listen", "DB.Path", "DB.BusyTimeout", "DB.MaxOpenConns", "Cache.Provider", "Cache.TTL", "Cache.CleanupInterval", "Auth.AccessTokenTTL", "Auth.RefreshTokenTTL", "Auth.Issuer", "Auth.SingleSession", "AgentConnection.DialTimeout", "AgentConnection.RequestTimeout", "AgentConnection.WriteQueueSize", "AgentConnection.MaxMessageBytes", "EventStream.SubscriberQueueSize", "ShutdownTimeout"} {
		if err := v.BindEnv(key); err != nil {
			return nil, err
		}
	}
	c := &Config{}
	if err := v.Unmarshal(&c.Server); err != nil {
		return nil, fmt.Errorf("cannot decode server config")
	}
	c.Database.Path = value(v, "DB.Path", "data/echo-service.db")
	c.Database.MaxOpenConns = integer(v, "DB.MaxOpenConns", 1)
	c.Cache.Provider = value(v, "Cache.Provider", "memory")
	c.Auth.Issuer = value(v, "Auth.Issuer", "echo-service")
	if v.IsSet("Auth.SingleSession") {
		single, err := strconv.ParseBool(v.GetString("Auth.SingleSession"))
		if err != nil {
			return nil, fmt.Errorf("Auth.SingleSession must be a boolean")
		}
		c.Auth.SingleSession = single
	}
	c.AgentConnection.WriteQueueSize = integer(v, "AgentConnection.WriteQueueSize", 256)
	c.AgentConnection.MaxMessageBytes = int64(integer(v, "AgentConnection.MaxMessageBytes", 50<<20))
	c.EventStream.SubscriberQueueSize = integer(v, "EventStream.SubscriberQueueSize", 256)
	for _, item := range []struct {
		key, fallback string
		target        *time.Duration
	}{
		{"ShutdownTimeout", "10s", &c.ShutdownTimeout}, {"DB.BusyTimeout", "5s", &c.Database.BusyTimeout},
		{"Cache.TTL", "5m", &c.Cache.TTL}, {"Cache.CleanupInterval", "1m", &c.Cache.CleanupInterval},
		{"Auth.AccessTokenTTL", "24h", &c.Auth.AccessTokenTTL}, {"Auth.RefreshTokenTTL", "168h", &c.Auth.RefreshTokenTTL},
		{"AgentConnection.DialTimeout", "5s", &c.AgentConnection.DialTimeout}, {"AgentConnection.RequestTimeout", "30s", &c.AgentConnection.RequestTimeout},
	} {
		d, err := time.ParseDuration(value(v, item.key, item.fallback))
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("%s must be a positive duration", item.key)
		}
		*item.target = d
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}
func (c *Config) Validate() error {
	if c.Server.AppName == "" || c.Server.AppCode <= 0 {
		return fmt.Errorf("AppName and positive AppCode are required")
	}
	switch c.Server.RunMode {
	case "debug", "test", "release":
	default:
		return fmt.Errorf("invalid RunMode")
	}
	_, port, err := net.SplitHostPort(c.Server.HTTPServer.Listen)
	if err != nil {
		return fmt.Errorf("HTTPServer.Listen must be host:port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("invalid HTTPServer.Listen port")
	}
	h := c.Server.HTTPServer
	if h.ReadTimeout < 0 || h.WriteTimeout < 0 || h.IdleTimeout < 0 || h.MaxHeaderBytes < 0 {
		return fmt.Errorf("HTTP limits cannot be negative")
	}
	if h.ConsoleEnable || h.AltoHandlerAPIError || h.PprofEnable || h.SwaggerEnable || h.IdempotenceEnable || h.APIBasePath != "" || len(h.Assets) > 0 {
		return fmt.Errorf("unsupported framework switches enabled")
	}
	if h.AuthType != "Fake" {
		return fmt.Errorf("keep framework AuthType=Fake; Echo APIs apply independent authentication")
	}
	if strings.TrimSpace(c.Database.Path) == "" || c.Database.BusyTimeout <= 0 || c.Database.MaxOpenConns != 1 {
		return fmt.Errorf("SQLite requires path, positive busy timeout and MaxOpenConns=1")
	}
	if c.Cache.Provider != "memory" || c.Cache.TTL <= 0 || c.Cache.CleanupInterval <= 0 {
		return fmt.Errorf("only memory cache is implemented; TTL and cleanup must be positive")
	}
	if c.Auth.AccessTokenTTL <= 0 || c.Auth.RefreshTokenTTL <= c.Auth.AccessTokenTTL || c.Auth.Issuer == "" {
		return fmt.Errorf("invalid auth configuration")
	}
	if c.AgentConnection.DialTimeout <= 0 || c.AgentConnection.RequestTimeout <= 0 || c.AgentConnection.WriteQueueSize < 1 || c.AgentConnection.MaxMessageBytes < 1024 || c.EventStream.SubscriberQueueSize < 1 || c.EventStream.SubscriberQueueSize > 4096 {
		return fmt.Errorf("invalid agent connection limits")
	}
	return nil
}
func value(v *viper.Viper, key, fallback string) string {
	if s := strings.TrimSpace(v.GetString(key)); s != "" {
		return s
	}
	return fallback
}
func integer(v *viper.Viper, key string, fallback int) int {
	if !v.IsSet(key) {
		return fallback
	}
	return v.GetInt(key)
}
