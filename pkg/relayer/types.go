package relayer

import "encoding/json"

// 交易类型常量（与 relayer-service/pkg/types 对齐）。
const (
	TxTypeSafe       = "SAFE"
	TxTypeSafeCreate = "SAFE-CREATE"
)

// Transaction 状态常量。终态为 STATE_CONFIRMED / STATE_FAILED / STATE_INVALID。
const (
	StateNew       = "STATE_NEW"
	StateExecuted  = "STATE_EXECUTED"
	StateMined     = "STATE_MINED"
	StateConfirmed = "STATE_CONFIRMED"
	StateInvalid   = "STATE_INVALID"
	StateFailed    = "STATE_FAILED"
)

// SafeCreateParams 是 SubmitRequest.signatureParams 在 SAFE-CREATE 类型时的载荷。
//
// 与 relayer-service/pkg/types.SafeCreateParams 字段一致；ScopeId 必须填，因为
// 后端在 EIP-712 typed data
//
//	CreateProxy(address paymentToken,uint256 payment,address paymentReceiver,bytes32 scopeId)
//
// 中要带它做 verify。
type SafeCreateParams struct {
	PaymentToken    string `json:"paymentToken"`
	Payment         string `json:"payment"`
	PaymentReceiver string `json:"paymentReceiver"`
	ScopeId         string `json:"scopeId"`
}

// SafeTxParams 是 SubmitRequest.signatureParams 在 SAFE 类型时的载荷。
//
// 与 relayer-service/pkg/types.SafeTxParams 一致。SDK 高层 ExecuteSafeTx 默认
// 全部走零值（gasPrice=0 / safeTxnGas=0 / baseGas=0 / gasToken=0x0 /
// refundReceiver=0x0 / operation=0=CALL），与 mm V2 的 submitSafeTx 一致。
type SafeTxParams struct {
	GasPrice       string `json:"gasPrice"`
	Operation      string `json:"operation"`
	SafeTxnGas     string `json:"safeTxnGas"`
	BaseGas        string `json:"baseGas"`
	GasToken       string `json:"gasToken"`
	RefundReceiver string `json:"refundReceiver"`
}

// SubmitRequest 是 POST /submit 请求体。字段顺序与 relayer-service handler 校验路径一致。
type SubmitRequest struct {
	From            string          `json:"from"`
	To              string          `json:"to"`
	ProxyWallet     string          `json:"proxyWallet"`
	Data            string          `json:"data,omitempty"`
	Nonce           string          `json:"nonce,omitempty"`
	Signature       string          `json:"signature"`
	SignatureParams json.RawMessage `json:"signatureParams"`
	Type            string          `json:"type"`
	ScopeID         string          `json:"scopeId"`
}

// SubmitResponse 是 POST /submit 响应。提交时仅 TransactionID 稳定，
// TransactionHash 在终态前可能因后台 gas bump 改变（见 relayer-service CLAUDE）。
type SubmitResponse struct {
	TransactionID   string `json:"transactionID"`
	TransactionHash string `json:"transactionHash"`
	State           string `json:"state"`
}

// Transaction 是 GET /transaction 响应（relayer-service 端的 RelayerTransaction 子集）。
//
// 只暴露调用方常用字段；完整 schema 在 relayer-service/pkg/types.RelayerTransaction。
// 如需扩展用 json.RawMessage 接整体再二次解析。
type Transaction struct {
	TransactionID   string `json:"transactionID"`
	TransactionHash string `json:"transactionHash"`
	From            string `json:"from"`
	To              string `json:"to"`
	ProxyAddress    string `json:"proxyAddress"`
	Data            string `json:"data"`
	Nonce           string `json:"nonce"`
	Signature       string `json:"signature"`
	State           string `json:"state"`
	Type            string `json:"type"`
	ScopeID         string `json:"scopeId,omitempty"`
	Owner           string `json:"owner"`
	GasUsed         uint64 `json:"gasUsed,omitempty"`
	GasPrice        string `json:"gasPrice,omitempty"`
	BlockNumber     uint64 `json:"blockNumber,omitempty"`
	ErrorMessage    string `json:"errorMessage,omitempty"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

// nonceResponse 是 GET /nonce 响应（内部解码用）。
type nonceResponse struct {
	Nonce string `json:"nonce"`
}

// deployedResponse 是 GET /deployed 响应（内部解码用）。
type deployedResponse struct {
	Deployed bool   `json:"deployed"`
	Address  string `json:"address"`
}

// RelayPayload 是 GET /relay-payload 响应。Address = relayer 下笔交易的 gas
// 付费地址；Nonce = 该地址 pending nonce（eth_getTransactionCount("pending")）。
type RelayPayload struct {
	Address string `json:"address"`
	Nonce   uint64 `json:"nonce"`
}
