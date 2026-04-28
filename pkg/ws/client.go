package ws

// Client 是 ws 子包客户端。Phase 1 占位结构。
//
// TODO Phase 5: 实现指数退避重连（1s→30s）/ Sequence guard / SubscribeBook /
// SubscribeOrders；channel 端推 Type=RESET 通知消费方清缓存。
type Client struct{}

// NewStub 构造 Phase 1 占位 Client。
//
// TODO Phase 5: 替换为 New(cfg ClientConfig) (*Client, error)。
func NewStub() *Client {
	return &Client{}
}
