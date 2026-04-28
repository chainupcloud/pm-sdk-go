package pmsdkgo

import "github.com/chainupcloud/pm-sdk-go/pkg/obs"

// Logger 是 SDK 的轻量日志钩子接口（契约 §9）。
//
// 真实定义在 pkg/obs.Logger；顶层用 type alias 暴露，让 `pmsdkgo.Logger`
// 与 `obs.Logger` 完全等价，调用方两处导入皆可。
type Logger = obs.Logger

// Metrics 是 SDK 的轻量指标钩子接口（契约 §9）。
//
// 真实定义在 pkg/obs.Metrics；alias 同 Logger。
type Metrics = obs.Metrics
