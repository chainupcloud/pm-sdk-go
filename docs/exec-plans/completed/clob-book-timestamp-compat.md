---
issue: https://github.com/chainupcloud/pm-sdk-go/issues/34
status: completed
---

# CLOB book timestamp compatibility

## 目标

让 CLOB 单笔、批量订单簿响应在一次 HTTP 请求内兼容真实 Hermes wire：RFC3339 字符串 timestamp，以及历史数字秒/毫秒 timestamp；不修改生成文件，不通过二次 HTTP 请求兼容 JSON 类型。

## 执行步骤

- [x] 先补 string timestamp 解码失败、单请求次数和批量响应回归测试。
- [x] 在 `pkg/clob` 手写扩展中实现 `OrderBookSummary.UnmarshalJSON`，兼容真实字符串字段和历史数字字段。
- [x] 明确 timestamp 缺失、非法值和秒/毫秒单位行为。
- [x] 更新真实契约 fixture、Changelog 与 API 文档。
- [x] 运行 `gofmt`、`go test ./...`、`go vet ./...` 和 codegen drift 检查。
- [x] 创建 PR，绑定 Issue #34 与 M23，并确认 CI 全绿。

## 验收标准

- RFC3339、RFC3339Nano、数字秒、数字毫秒以及对应数字字符串均可解析。
- `/book` 与 `/books` 共用同一兼容解码逻辑。
- `GetBook` 单次调用只发送一次 HTTP 请求。
- 缺失/null timestamp 映射为零时间；非法 timestamp 返回可识别的 `ErrUpstream` 解码错误。
- 真实 Hermes 的字符串 `min_order_size`、`tick_size` 不再阻断整本解析。

## Change Log

- 2026-08-04：用户明确要求使用 goal 实现 pm-sdk-go #34，确认本执行范围。
- 2026-08-04：真实 Hermes `/book` 抽样确认 timestamp 为 RFC3339 字符串；兼容范围扩展为 RFC3339Nano、Unix 秒/毫秒数字及数字字符串。
- 2026-08-04：本地全量测试、contract tests、vet、固定版 golangci-lint 与 diff 检查通过。codegen 检查发现上游 OpenAPI 已存在与 #34 无关的生成漂移，本 PR 不混入该漂移。
- 2026-08-04：PR #35 已创建并绑定 M23，首轮 lint、contract tests、build/vet/test CI 全绿；执行计划归档，等待 merge commit 合并。
