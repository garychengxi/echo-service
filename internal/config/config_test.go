package config

import (
	"github.com/spf13/viper"
	"strings"
	"testing"
	"time"
)

func load(t *testing.T, extra string) (*Config, error) {
	t.Helper()
	v := viper.New()
	v.SetConfigType("toml")
	if err := v.ReadConfig(strings.NewReader("AppName='echo'\nAppCode=100\nRunMode='release'\n" + extra)); err != nil {
		t.Fatal(err)
	}
	v.SetDefault("HTTPServer.Listen", "127.0.0.1:9090")
	v.SetDefault("HTTPServer.AuthType", "Fake")
	return Load(v)
}
func TestDefaultsAndEnv(t *testing.T) {
	t.Setenv("ECHO_DB_PATH", t.TempDir()+"/db.sqlite")
	c, e := load(t, "")
	if e != nil {
		t.Fatal(e)
	}
	if c.Database.MaxOpenConns != 1 || c.Cache.Provider != "memory" || c.Auth.AccessTokenTTL != 30*time.Minute {
		t.Fatal("invalid defaults")
	}
	if !strings.Contains(c.Database.Path, "db.sqlite") {
		t.Fatal("env override ignored")
	}
}
func TestInvalid(t *testing.T) {
	for _, extra := range []string{"ShutdownTimeout='0s'", "[HTTPServer]\nListen='invalid'", "[DB]\nMaxOpenConns=10", "[Cache]\nProvider='redis'", "[Auth]\nAccessTokenTTL='200h'", "[HTTPServer]\nAltoHandlerAPIError=true"} {
		if _, e := load(t, extra); e == nil {
			t.Fatalf("accepted invalid config %q", extra)
		}
	}
}
