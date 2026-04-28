package obs

// Logger 是 SDK 内部使用的轻量结构化日志接口。
//
// 与 zap.SugaredLogger 风格对齐：消息文本 + 交替 key/value。SDK 自身只在
// HTTP / WS / signer 关键挂点调本接口；用户可注入 zap / logrus / 自家封装。
//
// 实现要求：
//   - 实现方应保证无 panic（即使 kvs 长度奇数也容错）
//   - 实现方应 goroutine-safe（SDK 多 goroutine 复用同一实例）
//   - 性能敏感路径不应在 Debug 关闭时仍格式化字段；推荐 zap 这种延迟格式化实现
type Logger interface {
	Debugw(msg string, kvs ...any)
	Infow(msg string, kvs ...any)
	Warnw(msg string, kvs ...any)
	Errorw(msg string, kvs ...any)
}

// NopLogger 是默认 Logger 实现：所有方法 no-op，零分配。
//
// SDK 在 WithLogger 未注入时自动使用 NopLogger；用户也可显式构造（例如
// 单测里替换掉 zap，避免日志泄漏到 stderr）。
type NopLogger struct{}

// Debugw 实现 Logger。
func (NopLogger) Debugw(string, ...any) {}

// Infow 实现 Logger。
func (NopLogger) Infow(string, ...any) {}

// Warnw 实现 Logger。
func (NopLogger) Warnw(string, ...any) {}

// Errorw 实现 Logger。
func (NopLogger) Errorw(string, ...any) {}
