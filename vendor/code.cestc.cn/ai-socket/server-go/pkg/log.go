package core

import (
	"code.cestc.cn/ai-socket/logger-go"
	"context"
	"github.com/gookit/goutil/fsutil"
	"path/filepath"
)

var (
	HTTPLogger logger.Logger
	DBLogger   logger.Logger
)

const (
	configDirPath        = "logger"
	configPathForService = "logger/service.toml"
)

func init() {
	HTTPLogger, _ = logger.NewLogger(loggerConfig("http"))
	DBLogger, _ = logger.NewLogger(loggerConfig("db"))

	// set log config for service
	exists := fsutil.FileExists(filepath.Join("conf", configPathForService))
	if exists {
		err := logger.SetDefaultConfigWithFile(configPathForService)
		if err != nil {
			logger.Errorf(context.Background(), "LoggerSetConfigFileError path: %s, err: %v", configPathForService, err)
		}
	}
}

func loggerConfig(fileName string) *logger.Config {
	filePath := filepath.Join(configDirPath, fileName+".toml")
	exists := fsutil.FileExists(filepath.Join("conf", filePath))
	if exists {
		config, err := logger.ParseConfigFile(filePath)
		if err == nil {
			return config
		}
		logger.Errorf(context.Background(), "LoggerSetConfigFileError path: %s, err: %v", filePath, err)
	}

	return &logger.Config{
		Outputs: []logger.Output{
			{
				Type:  "Console",
				Level: "Debug",
			},
			{
				Type:       "File",
				Level:      "Info",
				Path:       "./logs/trace/info.log",
				Rotation:   true,
				MaxAge:     7,
				MaxBackups: 5,
			},
			{
				Type:       "File",
				Level:      "Warn",
				Path:       "./logs/trace/error.log",
				Rotation:   true,
				MaxAge:     30,
				MaxBackups: 10,
			},
		},
	}
}
