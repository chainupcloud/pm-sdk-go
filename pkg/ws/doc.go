// Package ws 提供 pm-cup2026 WebSocket 行情 / 订单订阅客户端（契约 §6）。
//
// 入口：[NewFacade] → *Facade.SubscribeBook / *Facade.SubscribeOrders。
//
// 重连语义：连接断开后指数退避重连（1s → 2s → 4s → 8s → 16s → 30s 封顶 +
// 0-500ms jitter）；重连成功后向 channel 推一个 Type=RESET 事件，消费方应清缓存
// 等待下一个 SNAPSHOT 重建。
//
// Sequence guard：上游 asyncapi spec 没有显式 sequence number，SDK 用
// 上游 timestamp（Unix ms）作为单调递增序号，配合 hash 字段实现 nonce guard
// 拦截重复 / 倒退帧。检测到异常时同样推 RESET。
//
// 协议参考：
//   - docs/asyncapi-market.json  /ws/market 公共行情
//   - docs/asyncapi-user.json    /ws/user 鉴权订单 / 成交
package ws
