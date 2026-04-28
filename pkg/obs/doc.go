// Package obs 暴露 SDK 的 observability 钩子接口（契约 §9）。
//
// 设计原则：
//
//   - 接口最小化：Logger 仅 4 级（Debug/Info/Warn/Error），Metrics 仅
//     IncCounter / ObserveHistogram 两个方法
//   - 默认无开销：未注入时 SDK 走 Nop 实现，编译期 inline 后无运行时分支
//   - 框架解耦：zap / prometheus 通过子包 zapobs / promobs 提供 adapter，
//     用户不引这些子包就不会把它们打进二进制
//
// 顶层 pmsdkgo.WithLogger / pmsdkgo.WithMetrics 接受本包接口。
package obs
