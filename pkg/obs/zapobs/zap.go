// Package zapobs 提供 obs.Logger 的 zap 适配器。
//
// 用户在自家代码里构造 *zap.Logger（或 *zap.SugaredLogger），然后 New 把它
// 包成 obs.Logger 注入 SDK：
//
//	logger, _ := zap.NewProduction()
//	cli, _ := pmsdkgo.New(pmsdkgo.WithLogger(zapobs.New(logger)))
//
// SDK 不直接依赖 zap：用户不引本子包就不会把 zap 打进二进制，go.mod 里
// zap 仅以 indirect 形式存在（through this package's compile-time link）。
package zapobs

import (
	"go.uber.org/zap"

	"github.com/chainupcloud/pm-sdk-go/pkg/obs"
)

// New 把 *zap.Logger 包成 obs.Logger。
//
// 内部通过 Sugar() 走 zap 的延迟格式化路径：当 level 关闭时（如 production
// 关 Debug），kvs 不会被实际格式化，零分配开销。
func New(l *zap.Logger) obs.Logger {
	if l == nil {
		return obs.NopLogger{}
	}
	return &zapLogger{sugar: l.Sugar()}
}

// NewSugared 接受已有的 *zap.SugaredLogger（用户可能在别处已经 sugar 过）。
func NewSugared(s *zap.SugaredLogger) obs.Logger {
	if s == nil {
		return obs.NopLogger{}
	}
	return &zapLogger{sugar: s}
}

type zapLogger struct {
	sugar *zap.SugaredLogger
}

// Debugw 实现 obs.Logger。
func (z *zapLogger) Debugw(msg string, kvs ...any) { z.sugar.Debugw(msg, kvs...) }

// Infow 实现 obs.Logger。
func (z *zapLogger) Infow(msg string, kvs ...any) { z.sugar.Infow(msg, kvs...) }

// Warnw 实现 obs.Logger。
func (z *zapLogger) Warnw(msg string, kvs ...any) { z.sugar.Warnw(msg, kvs...) }

// Errorw 实现 obs.Logger。
func (z *zapLogger) Errorw(msg string, kvs ...any) { z.sugar.Errorw(msg, kvs...) }
