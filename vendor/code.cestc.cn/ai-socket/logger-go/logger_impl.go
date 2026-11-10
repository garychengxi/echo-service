package logger

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

type Field = zapcore.Field

func NewLoggerWithConfigFile(filePath string) (Logger, error) {
	config, err := ParseConfigFile(filePath)
	if err != nil {
		return nil, err
	}

	return NewLogger(config)
}

func NewLogger(config *Config) (Logger, error) {
	var cores []zapcore.Core
	for _, output := range config.Outputs {
		core, err := zapCoreForOutput(output)
		if err != nil {
			return nil, err
		}
		cores = append(cores, core)
	}

	globalLevel := zapcore.InfoLevel
	level := strings.ToLower(config.Level)
	if err := globalLevel.UnmarshalText([]byte(level)); err != nil {
		return nil, err
	}

	core := zapcore.NewTee(cores...)
	zapLogger := zap.New(core,
		zap.AddCaller(),
		zap.AddCallerSkip(3),
		zap.AddStacktrace(zapcore.FatalLevel))
	return newLogger(zapLogger), nil
}

func zapCoreForOutput(output Output) (zapcore.Core, error) {
	var level zapcore.Level
	if err := level.UnmarshalText([]byte(output.Level)); err != nil {
		return nil, err
	}

	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	var writer zapcore.WriteSyncer
	var encoder zapcore.Encoder
	switch strings.ToLower(output.Type) {
	case "file":
		if output.Rotation {
			writer = zapcore.AddSync(&lumberjack.Logger{
				Filename:   output.Path,
				MaxSize:    output.MaxSize,
				MaxBackups: output.MaxBackups,
				MaxAge:     output.MaxAge,
				Compress:   output.Compress,
				LocalTime:  true,
			})
		} else {
			file, err := os.OpenFile(output.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return nil, err
			}
			writer = zapcore.AddSync(file)
		}
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	case "console":
		writer = zapcore.AddSync(os.Stdout)
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	}

	// 创建级别过滤器
	levelEnabler := zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
		return lvl >= level
	})

	return zapcore.NewCore(encoder, writer, levelEnabler), nil
}

type loggerImpl struct {
	zapLogger *zap.Logger
}

func newLogger(l *zap.Logger) *loggerImpl {
	return &loggerImpl{
		zapLogger: l,
	}
}

func (l *loggerImpl) WithContext(ctx context.Context) *loggerImpl {
	if ctx == nil {
		return l
	}

	if fields := FromContext(ctx); len(fields) > 0 {
		l.zapLogger.With(fields...)
		return l
	}

	return l
}

func (l *loggerImpl) Debugf(ctx context.Context, message string, a ...any) {
	l.WithContext(ctx).zapLogger.Debug(fmt.Sprintf(message, a...))
}

func (l *loggerImpl) Infof(ctx context.Context, message string, a ...any) {
	l.WithContext(ctx).zapLogger.Info(fmt.Sprintf(message, a...))
}

func (l *loggerImpl) Warnf(ctx context.Context, message string, a ...any) {
	l.WithContext(ctx).zapLogger.Warn(fmt.Sprintf(message, a...))
}

func (l *loggerImpl) Errorf(ctx context.Context, message string, a ...any) {
	l.WithContext(ctx).zapLogger.Error(fmt.Sprintf(message, a...))
}

func (l *loggerImpl) Fatalf(ctx context.Context, message string, a ...any) {
	message = message + "\n" + string(debug.Stack())
	l.WithContext(ctx).zapLogger.Fatal(fmt.Sprintf(message, a...))
	os.Exit(-1)
}

func (l *loggerImpl) Sync() error {
	return l.zapLogger.Sync()
}

func (l *loggerImpl) Debug(ctx context.Context, message string, field ...Field) {
	l.WithContext(ctx).zapLogger.Debug(message, field...)
}
func (l *loggerImpl) Info(ctx context.Context, message string, field ...Field) {
	l.WithContext(ctx).zapLogger.Info(message, field...)
}
func (l *loggerImpl) Warn(ctx context.Context, message string, field ...Field) {
	l.WithContext(ctx).zapLogger.Warn(message, field...)
}
func (l *loggerImpl) Error(ctx context.Context, message string, field ...Field) {
	l.WithContext(ctx).zapLogger.Error(message, field...)
}
func (l *loggerImpl) Fatal(ctx context.Context, message string, field ...Field) {
	l.WithContext(ctx).zapLogger.Fatal(message, field...)
}
