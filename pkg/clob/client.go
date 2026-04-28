package clob

// Client 是 clob 子包客户端。Phase 1 占位结构。
//
// TODO Phase 3: 引入 oapi-codegen 生成的底层 Client、HTTP doer、签名 hook 等字段；
// 实现 PlaceOrder / CancelOrder / GetOrder / ListOrders / GetBook / GetTrades。
type Client struct{}

// NewStub 构造 Phase 1 占位 Client。
//
// TODO Phase 3: 替换为 New(cfg ClientConfig) (*Client, error)，接管真实依赖注入。
func NewStub() *Client {
	return &Client{}
}
