// Package clob 封装 pm-cup2026 clob-service 的下单 / 撤单 / 查询接口。
//
// 包内 generated.go 由 scripts/codegen.sh 通过 oapi-codegen 从上游 OpenAPI 生成，
// 提供低层 *Client、模型类型与 embedded spec。Phase 3 起将在本目录新增手写
// client.go / types.go，封装为契约 §4 的高层门面（PlaceOrder / CancelOrder 等）。
package clob
