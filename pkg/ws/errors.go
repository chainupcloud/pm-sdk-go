package ws

import "errors"

// 哨兵复用 pkg/clob（契约 §8 顶层 pmsdkgo.Err* 的真实定义）：SubscribeXxx 入参
// 校验失败时直接返回 %w clob.ErrPrecondition / clob.ErrSign（见 client.go），所以
// ws 包不再 alias clob 哨兵。
//
// 选择：与 pkg/gamma 同样不抽 internal/httpx。理由：
//   1. ws 主要错误来自连接生命周期（断线 / 序号跳变），HTTP 状态码映射并非主菜
//   2. 抽 internal 包会触动 pkg/clob 测试，带来 Surgical Changes 风险
//   3. Phase 6 signer 落地时再统一评估第三处去重
var (
	// ErrConnLost WebSocket 连接断开（重连前推 RESET 后会以本错误退出 Subscribe channel）。
	ErrConnLost = errors.New("pmsdkgo/ws: connection lost")
	// ErrSequenceJump 检测到 sequence 跳变（timestamp 倒退或 hash 异常重复）。
	// 仅在内部用于触发 RESET，不返回给消费方。
	ErrSequenceJump = errors.New("pmsdkgo/ws: sequence jump detected")
	// ErrSubscribeRejected 服务端拒绝订阅（subscribe 后立即 close 或返回错误 frame）。
	ErrSubscribeRejected = errors.New("pmsdkgo/ws: subscribe rejected by server")
)
