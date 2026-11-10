package logger

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"

	"go.uber.org/zap/zapcore"
)

type Logger interface {
	Debugf(ctx context.Context, message string, a ...any)
	Infof(ctx context.Context, message string, a ...any)
	Warnf(ctx context.Context, message string, a ...any)
	Errorf(ctx context.Context, message string, a ...any)
	Fatalf(ctx context.Context, message string, a ...any)

	Debug(ctx context.Context, message string, fields ...Field)
	Info(ctx context.Context, message string, fields ...Field)
	Warn(ctx context.Context, message string, fields ...Field)
	Error(ctx context.Context, message string, fields ...Field)
	Fatal(ctx context.Context, message string, fields ...Field)
	Sync() error
}

func Debugf(ctx context.Context, message string, a ...any) {
	Debug(ctx, fmt.Sprintf(message, a...), FieldRequestID(ctx))
}

func Infof(ctx context.Context, message string, a ...any) {
	Info(ctx, fmt.Sprintf(message, a...), FieldRequestID(ctx))
}

func Warnf(ctx context.Context, message string, a ...any) {
	Warn(ctx, fmt.Sprintf(message, a...), FieldRequestID(ctx))
}

func Errorf(ctx context.Context, message string, a ...any) {
	Error(ctx, fmt.Sprintf(message, a...), FieldRequestID(ctx))
}

func Fatalf(ctx context.Context, message string, a ...any) {
	message = message + "\n" + string(debug.Stack())
	Fatal(ctx, fmt.Sprintf(message, a...), FieldRequestID(ctx))
	os.Exit(-1)
}

func Debug(ctx context.Context, message string, fields ...Field) {
	DefaultLogger().Debug(ctx, message, fields...)
}
func Info(ctx context.Context, message string, fields ...Field) {
	DefaultLogger().Info(ctx, message, fields...)
}
func Warn(ctx context.Context, message string, fields ...Field) {
	DefaultLogger().Warn(ctx, message, fields...)
}
func Error(ctx context.Context, message string, fields ...Field) {
	DefaultLogger().Error(ctx, message, fields...)
}
func Fatal(ctx context.Context, message string, fields ...Field) {
	DefaultLogger().Fatal(ctx, message, fields...)
}

// Sync 刷新日志到磁盘
func Sync() error {
	return DefaultLogger().Sync()
}

func StringField(key string, value string) Field {
	return Field{Key: key, String: value, Type: zapcore.StringType}
}

func IntField(key string, value int) Field {
	return Field{Key: key, Integer: int64(value), Type: zapcore.Int64Type}
}

type ctxKey struct{}

// WithContext 向context注入日志字段
func WithContext(ctx context.Context, fields ...Field) context.Context {
	if len(fields) == 0 {
		return ctx
	}

	// 合并已有字段和新字段
	if existing := ctx.Value(ctxKey{}); existing != nil {
		if existingFields, ok := existing.([]Field); ok {
			fields = append(existingFields, fields...)
		}
	}

	return context.WithValue(ctx, ctxKey{}, fields)
}

// FromContext 从context获取日志字段
func FromContext(ctx context.Context) []Field {
	if ctx == nil {
		return nil
	}

	if fields, ok := ctx.Value(ctxKey{}).([]Field); ok {
		return fields
	}

	return nil
}
