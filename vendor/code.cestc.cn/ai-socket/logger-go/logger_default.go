package logger

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

const (
	KeyRequestID = "requestID"
)

var (
	defaultLogger Logger
	once          sync.Once
)

// 默认logger初始化配置
var defaultConfig = &Config{
	Outputs: []Output{
		{
			Type:  "Console",
			Level: "Debug",
		},
		{
			Type:       "File",
			Level:      "Info",
			Path:       "./logs/service/info.log",
			Rotation:   true,
			MaxAge:     7,
			MaxBackups: 5,
		},
		{
			Type:       "File",
			Level:      "Warn",
			Path:       "./logs/service/error.log",
			Rotation:   true,
			MaxAge:     30,
			MaxBackups: 10,
		},
	},
}

func SetDefaultConfigWithFile(filePath string) error {
	cfg, err := ParseConfigFile(filePath)
	if err != nil {
		return err
	}

	SetDefaultConfig(cfg)
	return nil
}

func SetDefaultConfig(cfg *Config) {
	defaultConfig = cfg
}

// DefaultLogger -- 懒加载
func DefaultLogger() Logger {
	once.Do(func() {
		var err error
		defaultLogger, err = NewLogger(defaultConfig)
		if err != nil {
			panic(err.Error()) // 这里不依赖外部输入，调试后能保证此处不报错
		}
	})
	return defaultLogger
}

func FieldRequestID(ctx context.Context) Field {
	return StringField(KeyRequestID, getRequestID(ctx))
}

func getRequestID(ctx context.Context) string {
	requestID := ctx.Value(KeyRequestID)
	if requestID == nil || requestID == "" {
		requestID = uuid.NewString()
	}
	return requestID.(string)
}
