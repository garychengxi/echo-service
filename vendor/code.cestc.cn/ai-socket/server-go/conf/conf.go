package conf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"code.cestc.cn/ai-socket/logger-go"
	"github.com/BurntSushi/toml"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"

	"code.cestc.cn/ai-socket/server-go/internalsdk"
	"code.cestc.cn/ai-socket/server-go/sdk"
)

const (
	EnvServerConfigPath = "SERVER_CONFIG_PATH"

	defaultConfigFilePath = "conf/server/config.toml"
	defaultDeployMode     = DeployModeCloud
)

var (
	defaultConfig *Config
	once          sync.Once
)

func Initialize() error {
	var err error

	once.Do(func() {
		pwd, _ := os.Getwd()
		logger.Debugf(context.Background(), "pwd: %s", pwd)

		defaultConfig, err = NewConfig()
		if err != nil {
			return
		}

		viper.WatchConfig()
		viper.OnConfigChange(onConfigChangedHandler)
	})

	return err
}

func Default() *Config {
	if defaultConfig == nil {
		err := Initialize()
		if err != nil {
			logger.Errorf(context.Background(), "InitConfigError: %v", err)
		}
	}
	return defaultConfig
}

func SetDefault(config *Config) error {
	if config == nil {
		return errors.New("config is nil")
	}

	if err := config.check(); err != nil {
		return fmt.Errorf("CheckConfigError: %v", err)
	}

	configByte, err := toml.Marshal(config)
	if err != nil {
		return fmt.Errorf("MarshalConfigError: %v", err)
	}

	if err = viper.MergeConfig(bytes.NewBuffer(configByte)); err != nil {
		return fmt.Errorf("MergeConfigError: %v", err)
	}

	// 只在第一次初始化时设置监听
	if defaultConfig == nil {
		defaultConfig = config
		defaultConfig.AllSettings = viper.AllSettings()
		defaultConfig.LoadMode = LoadModeLoaded

		// 只在第一次初始化时设置监听
		viper.WatchConfig()
		viper.OnConfigChange(onConfigChangedHandler)
	} else {
		// 更新现有配置
		defaultConfig.reset(config)
		defaultConfig.AllSettings = viper.AllSettings()
		defaultConfig.LoadMode = LoadModeLoaded
	}

	return nil
}

func NewConfig() (*Config, error) {
	serverConfigFilePath := getServerConfigFilePath()
	viper.SetConfigFile(serverConfigFilePath)
	if err := viper.MergeInConfig(); err != nil {
		return nil, fmt.Errorf("MergeInConfigError filePath: %s, err: %v", serverConfigFilePath, err)
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("UnmarshalConfigError: %v", err)
	}
	if err := config.check(); err != nil {
		return nil, fmt.Errorf("CheckConfigError: %v", err)
	}

	config.AllSettings = viper.AllSettings()
	return &config, nil
}

func RegisterConfigChanged(name string, f func(in fsnotify.Event)) {
	if defaultConfig.configChanged == nil {
		defaultConfig.configChanged = make(map[string]func(in fsnotify.Event))
	}
	defaultConfig.configChanged[name] = f
}

func RegisterConfigContentChanged(f func(newConfig *Config, changedKeys []string)) {
	defaultConfig.configContentChanged = append(defaultConfig.configContentChanged, f)
}

func GetClient(name string) *ClientConfig {
	if !viper.IsSet(name) {
		return nil
	}
	var config ClientConfig
	_ = viper.UnmarshalKey(name, &config)
	return &config
}

func GetInternalClient(name string) *internalsdk.InternalClientConfig {
	config := GetClient(name)
	if config == nil {
		return nil
	}

	return &internalsdk.InternalClientConfig{
		Config: &sdk.Config{
			Endpoint: config.Endpoint,
			Credentials: &sdk.Credentials{
				AuthType:        sdk.AuthTypeAKSK,
				AccessKeyId:     config.AccessKey,
				SecretAccessKey: config.SecretKey,
			},
			Timeout: 30,
		},
		EnableImpersonate: config.EnableImpersonate,
		EnableIdempotence: config.EnableIdempotence,
		EnableSTS:         config.EnableSTS,
		STSDuration:       config.STSDuration,
		STSACL:            config.STSACL,
	}
}

func onConfigChangedHandler(e fsnotify.Event) {
	logger.Infof(context.Background(), "ConfigFileChanged: %s", e.Name)

	if e.Op&fsnotify.Write != fsnotify.Write {
		return
	}

	// invoke hook functions
	for name, f := range defaultConfig.configChanged {
		if e.Name == name {
			f(e)
		}
	}

	filePath := getServerConfigFilePath()
	if e.Name != filePath {
		return
	}

	// reload config
	defaultConfig.LoadMode = LoadModeLoading

	currentConfig, err := NewConfig()
	if err != nil {
		logger.Errorf(context.Background(), "ConfigReloadError: %v", err)
		return
	}

	changedKeys := make([]string, 0)
	for k, v := range defaultConfig.AllSettings {
		if !reflect.DeepEqual(viper.Get(k), v) {
			changedKeys = append(changedKeys, k)
		}
	}

	for _, handler := range defaultConfig.configContentChanged {
		if handler != nil {
			handler(currentConfig, changedKeys)
		}
	}

	defaultConfig.reset(currentConfig)
	defaultConfig.AllSettings = viper.AllSettings()
	defaultConfig.LoadMode = LoadModeLoaded
}

func getServerConfigFilePath() string {
	filePath := defaultConfigFilePath
	if configFilePath := os.Getenv(EnvServerConfigPath); configFilePath != "" {
		filePath = configFilePath
	}
	return filePath
}
