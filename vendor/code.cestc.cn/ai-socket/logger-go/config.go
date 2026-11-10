package logger

import (
	"fmt"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Level   string
	Outputs []Output
}

type Output struct {
	Type       string
	Path       string
	Level      string
	Rotation   bool
	MaxSize    int
	MaxBackups int
	MaxAge     int
	Compress   bool
}

// ParseConfigFile -- 解析配置文件
func ParseConfigFile(filePath string) (*Config, error) {
	var config *Config
	_, err := toml.DecodeFile(filePath, &config)
	if err != nil {
		return nil, fmt.Errorf("failed to parse extended config: %v", err)
	}

	return config, nil
}
