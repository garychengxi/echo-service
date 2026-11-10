package server

import (
	"context"
	"strings"

	"code.cestc.cn/ai-socket/logger-go"
	"github.com/gookit/goutil/arrutil"
	_ "go.uber.org/automaxprocs"

	"code.cestc.cn/ai-socket/server-go/conf"
)

type Module interface {
	ConfigKey() string
	Initialize() error
	Close()
	Reload() error
}

type Bootstrap struct {
	Modules []Module
}

func NewBootstrap(modules ...Module) *Bootstrap {
	return &Bootstrap{
		Modules: modules,
	}
}

func (b *Bootstrap) Initialize() error {
	err := conf.Initialize()
	if err != nil {
		return err
	}

	for _, module := range b.Modules {
		if err = module.Initialize(); err != nil {
			return err
		}
	}

	conf.RegisterConfigContentChanged(func(newConfig *conf.Config, changedKeys []string) {
		err = b.Reload(changedKeys)
		if err != nil {
			logger.Errorf(context.Background(), "BootstrapReloadError: %v", err)
		}
	})

	return nil
}

func (b *Bootstrap) Close() {
	for _, module := range b.Modules {
		module.Close()
	}
}

func (b *Bootstrap) Reload(changedKeys []string) error {
	for _, module := range b.Modules {
		if !arrutil.Contains(changedKeys, strings.ToLower(module.ConfigKey())) {
			continue
		}
		if err := module.Reload(); err != nil {
			return err
		}
	}
	return nil
}
