package pmsdkgo

// Logger 是 SDK 内部的轻量日志钩子接口。
// 用户可注入 zap.SugaredLogger / logrus / 自家封装；默认 nop。
type Logger interface {
	Debugw(msg string, kvs ...any)
	Infow(msg string, kvs ...any)
	Warnw(msg string, kvs ...any)
	Errorw(msg string, kvs ...any)
}

// nopLogger 是默认的丢弃日志实现。
type nopLogger struct{}

func (nopLogger) Debugw(string, ...any) {}
func (nopLogger) Infow(string, ...any)  {}
func (nopLogger) Warnw(string, ...any)  {}
func (nopLogger) Errorw(string, ...any) {}
