// Package ws 提供 pm-cup2026 WebSocket 行情 / 订单订阅客户端。
//
// Phase 1：仅暴露 *Client 占位与 NewStub() 工厂；自动重连 / Sequence 跳跃 RESET
// 等语义在 Phase 5 落地。接口契约见 docs/contract.md §6。
package ws
