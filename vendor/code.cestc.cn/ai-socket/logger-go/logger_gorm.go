package logger

import (
	"context"
	"time"

	gormlogger "gorm.io/gorm/logger"
)

type DBLogger struct {
	Log                       Logger
	LogLevel                  gormlogger.LogLevel
	SlowThreshold             time.Duration
	ParameterizedQueries      bool
	IgnoreRecordNotFoundError bool
}

func NewGormLogger(log Logger) DBLogger {
	return DBLogger{
		Log:                       log,
		LogLevel:                  gormlogger.Info,
		SlowThreshold:             200 * time.Millisecond,
		ParameterizedQueries:      false,
		IgnoreRecordNotFoundError: false,
	}
}

func (l DBLogger) shouldLog(level gormlogger.LogLevel) bool {
	return l.Log != nil && l.LogLevel >= level
}

func (l DBLogger) log(ctx context.Context, level gormlogger.LogLevel, msg string, data ...interface{}) {
	if !l.shouldLog(level) {
		return
	}

	switch level {
	case gormlogger.Info:
		l.Log.Infof(ctx, msg, data...)
	case gormlogger.Warn:
		l.Log.Warnf(ctx, msg, data...)
	case gormlogger.Error:
		l.Log.Errorf(ctx, msg, data...)
	default:
		l.Log.Infof(ctx, msg, data...)
	}
}

func (l DBLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	l.LogLevel = level
	return l
}

func (l DBLogger) Info(ctx context.Context, msg string, data ...interface{}) {
	l.log(ctx, gormlogger.Info, msg, data...)
}

func (l DBLogger) Warn(ctx context.Context, msg string, data ...interface{}) {
	l.log(ctx, gormlogger.Warn, msg, data...)
}

func (l DBLogger) Error(ctx context.Context, msg string, data ...interface{}) {
	l.log(ctx, gormlogger.Error, msg, data...)
}

func (l DBLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if !l.shouldLog(gormlogger.Info) {
		return
	}

	sql, num := fc()
	l.Log.Debugf(ctx, "sql: %s, %d rows affected", sql, num)
}
