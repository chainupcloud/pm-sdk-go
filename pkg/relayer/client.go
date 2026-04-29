package relayer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// HTTPDoer 是最小 HTTP 抽象，*http.Client 直接满足；单测可换 mock。
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client 是 relayer-service 的 HTTP 客户端。线程安全（HTTPDoer 自身线程安全即可）。
//
// 鉴权方式（与 services/relayer-service/internal/auth/middleware.go 对齐）：
//   - WithAPIKey(key)  → 设 RELAYER_API_KEY 头（推荐：商户长期 API Key）
//   - WithBearer(jwt)  → 设 Authorization: Bearer <jwt> 头（gamma-service 短期 token）
//
// 两者都不设时，所有请求都不会带鉴权头，仅适合 /ok 等公开端点。
type Client struct {
	baseURL string
	doer    HTTPDoer
	apiKey  string
	bearer  string
}

// Option 是 Client 构造选项。
type Option func(*Client)

// WithHTTPClient 用自定义 HTTPDoer 覆盖默认 http.Client（默认 30s timeout）。
func WithHTTPClient(d HTTPDoer) Option {
	return func(c *Client) {
		if d != nil {
			c.doer = d
		}
	}
}

// WithAPIKey 设置 RELAYER_API_KEY 鉴权头。
func WithAPIKey(apiKey string) Option {
	return func(c *Client) {
		c.apiKey = apiKey
	}
}

// WithBearer 设置 Authorization: Bearer <token> 鉴权头。
func WithBearer(token string) Option {
	return func(c *Client) {
		c.bearer = token
	}
}

// NewClient 构造 Client。baseURL 例如 "https://relayer.example.com"。
func NewClient(baseURL string, opts ...Option) (*Client, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("%w: baseURL required", ErrInvalidConfig)
	}
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		doer:    &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c, nil
}

// applyAuth 把鉴权头写入 req。两个方法同时配置时优先 RELAYER_API_KEY（与
// relayer-service middleware 校验顺序一致）。
func (c *Client) applyAuth(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("RELAYER_API_KEY", c.apiKey)
		return
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
}

// do 是公共请求路径：构造 → 鉴权 → 发送 → 非 2xx 包装 → 返回 body 字节。
func (c *Client) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	var rdr io.Reader
	if len(body) > 0 {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ErrInvalidConfig, err)
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	c.applyAuth(req)

	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, wrapTransportError(ctx, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, wrapHTTPError(resp, respBody)
	}
	return respBody, nil
}

// Submit 调用 POST /submit。req.Signature / req.SignatureParams / req.Type /
// req.ScopeID 必须在调用前由 Service 或调用方填好；Client 不做业务校验，校验
// 由 relayer-service handler 执行（见 internal/api/handler.go）。
func (c *Client) Submit(ctx context.Context, req *SubmitRequest) (*SubmitResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("%w: nil submit request", ErrInvalidConfig)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal submit: %v", ErrInvalidConfig, err)
	}
	respBody, err := c.do(ctx, http.MethodPost, "/submit", body)
	if err != nil {
		return nil, err
	}
	var out SubmitResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("%w: decode SubmitResponse: %v", ErrUpstream, err)
	}
	return &out, nil
}

// GetNonce 调用 GET /nonce，返回 Safe 当前 nonce（uint64）。
//
// 模式 A：直接传 Safe 地址（safeAddress != ""）；模式 B：传 EOA + scopeId（signer != ""
// && scopeId != ""），由 relayer-service 在链上推导 Safe 地址再读 nonce。
//
// 与 mm V2 行为一致：解析失败、nonce 字段缺失时返回 0（relayer-service 在 Safe
// 未部署时会返回 nonce=0）。
func (c *Client) GetNonce(ctx context.Context, safeAddress, signer, scopeID string) (uint64, error) {
	q := url.Values{}
	if safeAddress != "" {
		q.Set("address", safeAddress)
	}
	if signer != "" {
		q.Set("signer", signer)
	}
	if scopeID != "" {
		q.Set("scopeId", scopeID)
	}
	if len(q) == 0 {
		return 0, fmt.Errorf("%w: GetNonce requires safeAddress, or signer+scopeId", ErrInvalidConfig)
	}
	respBody, err := c.do(ctx, http.MethodGet, "/nonce?"+q.Encode(), nil)
	if err != nil {
		return 0, err
	}
	var out nonceResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return 0, fmt.Errorf("%w: decode NonceResponse: %v", ErrUpstream, err)
	}
	if out.Nonce == "" {
		return 0, nil
	}
	n, err := strconv.ParseUint(out.Nonce, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: parse nonce %q: %v", ErrUpstream, out.Nonce, err)
	}
	return n, nil
}

// GetDeployed 调用 GET /deployed。
//
// 与 GetNonce 对称：用 safeAddress 直查，或用 signer+scopeId 让服务端 CREATE2
// 推导。返回 (deployed, predictedOrSafeAddress, error)；deployed=false 时 address
// 是 CREATE2 预测的未部署地址，调用方下一步可以 DeploySafe 触发 deploy。
func (c *Client) GetDeployed(ctx context.Context, safeAddress, signer, scopeID string) (bool, string, error) {
	q := url.Values{}
	if safeAddress != "" {
		q.Set("address", safeAddress)
	}
	if signer != "" {
		q.Set("signer", signer)
	}
	if scopeID != "" {
		q.Set("scopeId", scopeID)
	}
	if len(q) == 0 {
		return false, "", fmt.Errorf("%w: GetDeployed requires safeAddress, or signer+scopeId", ErrInvalidConfig)
	}
	respBody, err := c.do(ctx, http.MethodGet, "/deployed?"+q.Encode(), nil)
	if err != nil {
		return false, "", err
	}
	var out deployedResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return false, "", fmt.Errorf("%w: decode DeployedResponse: %v", ErrUpstream, err)
	}
	return out.Deployed, out.Address, nil
}

// GetTransaction 调用 GET /transaction?id=...。
//
// state 终态：STATE_CONFIRMED / STATE_FAILED / STATE_INVALID。在终态前
// transactionHash 可能因 gas bump 改变（见 relayer-service CLAUDE 客户端契约）。
func (c *Client) GetTransaction(ctx context.Context, txID string) (*Transaction, error) {
	if txID == "" {
		return nil, fmt.Errorf("%w: txID required", ErrInvalidConfig)
	}
	respBody, err := c.do(ctx, http.MethodGet, "/transaction?id="+url.QueryEscape(txID), nil)
	if err != nil {
		return nil, err
	}
	var out Transaction
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("%w: decode Transaction: %v", ErrUpstream, err)
	}
	return &out, nil
}

// GetTransactions 调用 GET /transactions?limit=&offset=。
//
// 鉴权地址（API Key / JWT 解出来的 address）作为 owner 过滤，无法跨地址查询，
// 因此本方法不接受 address 参数。
func (c *Client) GetTransactions(ctx context.Context, limit, offset int) ([]Transaction, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	path := "/transactions"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	respBody, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out []Transaction
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("%w: decode []Transaction: %v", ErrUpstream, err)
	}
	return out, nil
}

// GetRelayPayload 调用 GET /relay-payload?scopeId=...。
//
// 返回 relayer 下笔交易的 gas 付费地址 + 该地址的 pending nonce。调用方需要离线
// 预计算 tx hash 时用到。scopeId 为空时（JWT 鉴权场景）由服务端从 JWT claims 取。
func (c *Client) GetRelayPayload(ctx context.Context, scopeID string) (*RelayPayload, error) {
	path := "/relay-payload"
	if scopeID != "" {
		path += "?scopeId=" + url.QueryEscape(scopeID)
	}
	respBody, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out RelayPayload
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("%w: decode RelayPayload: %v", ErrUpstream, err)
	}
	return &out, nil
}
