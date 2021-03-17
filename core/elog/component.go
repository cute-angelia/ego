package elog

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/gotomicro/ego/core/econf"
	"github.com/gotomicro/ego/core/elog/ali"
	"github.com/gotomicro/ego/core/util/xcolor"
)

const (
	// DebugLevel logs are typically voluminous, and are usually disabled in
	// production.
	DebugLevel = zap.DebugLevel
	// InfoLevel is the default logging priority.
	InfoLevel = zap.InfoLevel
	// WarnLevel logs are more important than Info, but don't need individual
	// human review.
	WarnLevel = zap.WarnLevel
	// ErrorLevel logs are high-priority. If an application is running smoothly,
	// it shouldn't generate any error-Level logs.
	ErrorLevel = zap.ErrorLevel
	// PanicLevel logs a message, then panics.
	PanicLevel = zap.PanicLevel
	// FatalLevel logs a message, then calls os.Exit(1).
	FatalLevel = zap.FatalLevel
)

type (
	Func      func(string, ...zap.Field)
	Field     = zap.Field
	Level     = zapcore.Level
	Component struct {
		name          string
		desugar       *zap.Logger
		lv            *zap.AtomicLevel
		config        *Config
		sugar         *zap.SugaredLogger
		asyncStopFunc func() error
	}
)

var (
	// String alias for zap.String
	String = zap.String
	// Any alias for zap.Any
	Any = zap.Any
	// Int64 alias for zap.Int64
	Int64 = zap.Int64
	// Int alias for zap.Int
	Int = zap.Int
	// Int32 alias for zap.Int32
	Int32 = zap.Int32
	// Uint alias for zap.Uint
	Uint = zap.Uint
	// Duration alias for zap.Duration
	Duration = zap.Duration
	// Durationp alias for zap.Duration
	Durationp = zap.Durationp
	// Object alias for zap.Object
	Object = zap.Object
	// Namespace alias for zap.Namespace
	Namespace = zap.Namespace
	// Reflect alias for zap.Reflect
	Reflect = zap.Reflect
	// Skip alias for zap.Skip()
	Skip = zap.Skip()
	// ByteString alias for zap.ByteString
	ByteString = zap.ByteString
)

const (
	defaultAliFallbackCorePath = "ali.log"
)

// newRotateFileCore construct  a rotate file zapcore.Core
func newRotateFileCore(config *Config, lv zap.AtomicLevel) (zapcore.Core, CloseFunc) {
	// Debug output to console and file by default
	cf := noopCloseFunc
	var ws = zapcore.AddSync(newRotate(config))
	if config.Debug {
		ws = zap.CombineWriteSyncers(os.Stdout, ws)
	}
	if config.EnableAsync {
		ws, cf = Buffer(ws, config.FlushBufferSize, config.FlushBufferInterval)
	}
	core := zapcore.NewCore(
		func() zapcore.Encoder {
			if config.Debug {
				return zapcore.NewConsoleEncoder(*config.encoderConfig)
			}
			return zapcore.NewJSONEncoder(*config.encoderConfig)
		}(),
		ws,
		lv,
	)
	return core, cf
}

// newAliCore construct a ali SLS zapcore.Core
func newAliCore(config *Config, lv zap.AtomicLevel) (zapcore.Core, CloseFunc) {
	c := *config
	c.Name = defaultAliFallbackCorePath
	fallbackCore, fallbackCoreCf := newRotateFileCore(&c, lv)
	core, cf := ali.NewCore(
		ali.WithEncoder(ali.NewMapObjEncoder(*config.encoderConfig)),
		ali.WithEndpoint(config.AliEndpoint),
		ali.WithAccessKeyID(config.AliAccessKeyID),
		ali.WithAccessKeySecret(config.AliAccessKeySecret),
		ali.WithProject(config.AliProject),
		ali.WithLogstore(config.AliLogstore),
		ali.WithLevelEnabler(lv),
		ali.WithFlushBufferSize(config.FlushBufferSize),
		ali.WithFlushBufferInterval(config.FlushBufferInterval),
		ali.WithApiBulkSize(config.AliApiBulkSize),
		ali.WithApiTimeout(config.AliApiTimeout),
		ali.WithApiRetryCount(config.AliApiRetryCount),
		ali.WithApiRetryWaitTime(config.AliApiRetryWaitTime),
		ali.WithApiRetryMaxWaitTime(config.AliApiRetryMaxWaitTime),
		ali.WithFallbackCore(fallbackCore),
	)
	return core, func() (err error) {
		if e := cf(); e != nil {
			err = fmt.Errorf("exec close func fail, %w ", e)
		}
		if e := fallbackCoreCf(); e != nil {
			err = fmt.Errorf("exec fallbackCore close func fail, %w", e)
		}
		return
	}
}

func newCore(config *Config, lv zap.AtomicLevel) (zapcore.Core, CloseFunc) {
	if config.Writer == writerRotateFile {
		return newRotateFileCore(config, lv)
	}
	if config.Writer == writerAliSLS {
		return newAliCore(config, lv)
	}
	return nil, nil
}

func newLogger(name string, config *Config) *Component {
	zapOptions := make([]zap.Option, 0)
	zapOptions = append(zapOptions, zap.AddStacktrace(zap.DPanicLevel))
	if config.EnableAddCaller {
		zapOptions = append(zapOptions, zap.AddCaller(), zap.AddCallerSkip(config.CallerSkip))
	}
	if len(config.fields) > 0 {
		zapOptions = append(zapOptions, zap.Fields(config.fields...))
	}

	lv := zap.NewAtomicLevelAt(zapcore.InfoLevel)
	if err := lv.UnmarshalText([]byte(config.Level)); err != nil {
		panic(err)
	}
	core, asyncStopFunc := newCore(config, lv)
	zapLogger := zap.New(core, zapOptions...)
	return &Component{
		desugar:       zapLogger,
		lv:            &lv,
		config:        config,
		sugar:         zapLogger.Sugar(),
		name:          name,
		asyncStopFunc: asyncStopFunc,
	}
}

// AutoLevel ...
func (logger *Component) AutoLevel(confKey string) {
	econf.OnChange(func(config *econf.Configuration) {
		lvText := strings.ToLower(config.GetString(confKey))
		if lvText != "" {
			logger.Info("update level", String("level", lvText), String("name", logger.config.Name))
			logger.lv.UnmarshalText([]byte(lvText))
		}
	})
}

// SetLevel ...
func (logger *Component) SetLevel(lv Level) {
	logger.lv.SetLevel(lv)
}

// Flush ...
// When use os.Stdout or os.Stderr as zapcore.WriteSyncer
// logger.desugar.Sync() maybe return an error like this: 'sync /dev/stdout: The handle is invalid.'
// Because os.Stdout and os.Stderr is a non-normal file, maybe not support 'fsync' in different os platform
// So ignored Sync() return value
// About issues: https://github.com/uber-go/zap/issues/328
// About 'fsync': https://man7.org/linux/man-pages/man2/fsync.2.html
func (logger *Component) Flush() error {
	if logger.asyncStopFunc != nil {
		if err := logger.asyncStopFunc(); err != nil {
			return err
		}
	}

	logger.desugar.Sync()
	return nil
}

// DefaultZapConfig ...
func DefaultZapConfig() *zapcore.EncoderConfig {
	return &zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "lv",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stack",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     timeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
}

func DefaultDebugConfig() *zapcore.EncoderConfig {
	return &zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "lv",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stack",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    DebugEncodeLevel,
		EncodeTime:     timeDebugEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
}

// DebugEncodeLevel ...
func DebugEncodeLevel(lv zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
	var colorize = xcolor.Red
	switch lv {
	case zapcore.DebugLevel:
		colorize = xcolor.Blue
	case zapcore.InfoLevel:
		colorize = xcolor.Green
	case zapcore.WarnLevel:
		colorize = xcolor.Yellow
	case zapcore.ErrorLevel, zap.PanicLevel, zap.DPanicLevel, zap.FatalLevel:
		colorize = xcolor.Red
	default:
	}
	enc.AppendString(colorize(lv.CapitalString()))
}

func timeEncoder(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendInt64(t.Unix())
}

func timeDebugEncoder(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString(t.Format("2006-01-02 15:04:05"))
}

// IsDebugMode ...
func (logger *Component) IsDebugMode() bool {
	return logger.config.Debug
}

func normalizeMessage(msg string) string {
	return fmt.Sprintf("%-32s", msg)
}

// Debug ...
func (logger *Component) Debug(msg string, fields ...Field) {
	if logger.IsDebugMode() {
		msg = normalizeMessage(msg)
	}
	logger.desugar.Debug(msg, fields...)
}

// Debugw ...
func (logger *Component) Debugw(msg string, keysAndValues ...interface{}) {
	if logger.IsDebugMode() {
		msg = normalizeMessage(msg)
	}
	logger.sugar.Debugw(msg, keysAndValues...)
}

func sprintf(template string, args ...interface{}) string {
	msg := template
	if msg == "" && len(args) > 0 {
		msg = fmt.Sprint(args...)
	} else if msg != "" && len(args) > 0 {
		msg = fmt.Sprintf(template, args...)
	}
	return msg
}

// StdLog ...
func (logger *Component) StdLog() *log.Logger {
	return zap.NewStdLog(logger.desugar)
}

// Debugf ...
func (logger *Component) Debugf(template string, args ...interface{}) {
	logger.sugar.Debugw(sprintf(template, args...))
}

// Info ...
func (logger *Component) Info(msg string, fields ...Field) {
	if logger.IsDebugMode() {
		msg = normalizeMessage(msg)
	}
	logger.desugar.Info(msg, fields...)
}

// Infow ...
func (logger *Component) Infow(msg string, keysAndValues ...interface{}) {
	if logger.IsDebugMode() {
		msg = normalizeMessage(msg)
	}
	logger.sugar.Infow(msg, keysAndValues...)
}

// Infof ...
func (logger *Component) Infof(template string, args ...interface{}) {
	logger.sugar.Infof(sprintf(template, args...))
}

// Warn ...
func (logger *Component) Warn(msg string, fields ...Field) {
	if logger.IsDebugMode() {
		msg = normalizeMessage(msg)
	}
	logger.desugar.Warn(msg, fields...)
}

// Warnw ...
func (logger *Component) Warnw(msg string, keysAndValues ...interface{}) {
	if logger.IsDebugMode() {
		msg = normalizeMessage(msg)
	}
	logger.sugar.Warnw(msg, keysAndValues...)
}

// Warnf ...
func (logger *Component) Warnf(template string, args ...interface{}) {
	logger.sugar.Warnf(sprintf(template, args...))
}

// Error ...
func (logger *Component) Error(msg string, fields ...Field) {
	if logger.IsDebugMode() {
		msg = normalizeMessage(msg)
	}
	logger.desugar.Error(msg, fields...)
}

// Errorw ...
func (logger *Component) Errorw(msg string, keysAndValues ...interface{}) {
	if logger.IsDebugMode() {
		msg = normalizeMessage(msg)
	}
	logger.sugar.Errorw(msg, keysAndValues...)
}

// Errorf ...
func (logger *Component) Errorf(template string, args ...interface{}) {
	logger.sugar.Errorf(sprintf(template, args...))
}

// Panic ...
func (logger *Component) Panic(msg string, fields ...Field) {
	if logger.IsDebugMode() {
		panicDetail(msg, fields...)
		msg = normalizeMessage(msg)
	}
	logger.desugar.Panic(msg, fields...)
}

// Panicw ...
func (logger *Component) Panicw(msg string, keysAndValues ...interface{}) {
	if logger.IsDebugMode() {
		msg = normalizeMessage(msg)
	}
	logger.sugar.Panicw(msg, keysAndValues...)
}

// Panicf ...
func (logger *Component) Panicf(template string, args ...interface{}) {
	logger.sugar.Panicf(sprintf(template, args...))
}

// DPanic ...
func (logger *Component) DPanic(msg string, fields ...Field) {
	if logger.IsDebugMode() {
		panicDetail(msg, fields...)
		msg = normalizeMessage(msg)
	}
	logger.desugar.DPanic(msg, fields...)
}

// DPanicw ...
func (logger *Component) DPanicw(msg string, keysAndValues ...interface{}) {
	if logger.IsDebugMode() {
		msg = normalizeMessage(msg)
	}
	logger.sugar.DPanicw(msg, keysAndValues...)
}

// DPanicf ...
func (logger *Component) DPanicf(template string, args ...interface{}) {
	logger.sugar.DPanicf(sprintf(template, args...))
}

// Fatal ...
func (logger *Component) Fatal(msg string, fields ...Field) {
	if logger.IsDebugMode() {
		panicDetail(msg, fields...)
		msg = normalizeMessage(msg)
		return
	}
	logger.desugar.Fatal(msg, fields...)
}

// Fatalw ...
func (logger *Component) Fatalw(msg string, keysAndValues ...interface{}) {
	if logger.IsDebugMode() {
		msg = normalizeMessage(msg)
	}
	logger.sugar.Fatalw(msg, keysAndValues...)
}

// Fatalf ...
func (logger *Component) Fatalf(template string, args ...interface{}) {
	logger.sugar.Fatalf(sprintf(template, args...))
}

func panicDetail(msg string, fields ...Field) {
	enc := zapcore.NewMapObjectEncoder()
	for _, field := range fields {
		field.AddTo(enc)
	}

	// 控制台输出
	fmt.Printf("%s: \n    %s: %s\n", xcolor.Red("panic"), xcolor.Red("msg"), msg)
	if _, file, line, ok := runtime.Caller(3); ok {
		fmt.Printf("    %s: %s:%d\n", xcolor.Red("loc"), file, line)
	}
	for key, val := range enc.Fields {
		fmt.Printf("    %s: %s\n", xcolor.Red(key), fmt.Sprintf("%+v", val))
	}
}

// With ...
func (logger *Component) With(fields ...Field) *Component {
	desugarLogger := logger.desugar.With(fields...)
	return &Component{
		desugar: desugarLogger,
		lv:      logger.lv,
		sugar:   desugarLogger.Sugar(),
		config:  logger.config,
	}
}

// GetConfigDir 获取日志路径
func (logger *Component) GetConfigDir() string {
	return logger.config.Dir
}

// GetConfigName 获取日志名称
func (logger *Component) GetConfigName() string {
	return logger.config.Name
}
