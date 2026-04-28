package gamma

// Client 是 gamma 子包客户端。Phase 1 占位结构。
//
// TODO Phase 4: 引入 oapi-codegen 生成的底层 Client；实现
// GetEvent / ListEvents / GetMarket / GetToken。
type Client struct{}

// NewStub 构造 Phase 1 占位 Client。
//
// TODO Phase 4: 替换为 New(cfg ClientConfig) (*Client, error)。
func NewStub() *Client {
	return &Client{}
}
