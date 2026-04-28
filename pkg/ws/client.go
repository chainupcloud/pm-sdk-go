package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/shopspring/decimal"

	"github.com/chainupcloud/pm-sdk-go/pkg/clob"
	"github.com/chainupcloud/pm-sdk-go/pkg/obs"
)

// Facade 是 ws 业务门面（契约 §6）。
//
// 命名沿用 Phase 3/4 约定（clob.Facade / gamma.Facade），避让 generated 名字。
// ws 没有 codegen，但保持一致让顶层 Client 字段类型对称。
//
// 内部职责：
//  1. 管理 wsURL / dialer / 心跳间隔等连接配置
//  2. 每次 SubscribeBook / SubscribeOrders 调用起独立 goroutine 跑 connect-loop
//  3. connect-loop = dial → 发 subscribe envelope → read loop → 断线指数退避重连
//  4. 重连成功后推 BookUpdate{Type:RESET} / OrderUpdate（contextually）通知消费方
//  5. nonce guard：内部记录 last (timestamp, hash) 拒重复 & 检测跳跃 → RESET
type Facade struct {
	wsURL string

	dialOpts     *websocket.DialOptions
	pingInterval time.Duration
	maxBackoff   time.Duration

	// nowFn / sleepFn / dialFn 是测试 hook；生产路径用 stdlib 默认。
	nowFn  func() time.Time
	dialFn func(ctx context.Context, url string, opts *websocket.DialOptions) (*websocket.Conn, *http.Response, error)

	// jitterFn 返回 0-500ms 抖动；测试可注入固定 0。
	jitterFn func() time.Duration

	// userAuth 是用户频道凭证；nil 时 SubscribeOrders 返回 ErrSign。
	userAuth *UserAuth

	// logger / metrics 由 WithLogger / WithMetrics 注入；默认 Nop 实现。
	logger  obs.Logger
	metrics obs.Metrics
}

// FacadeOption 是 Facade 构造选项。
type FacadeOption func(*Facade)

// UserAuth 是 SubscribeOrders 必需的 API 凭证（asyncapi-user.json）。
type UserAuth struct {
	APIKey     string
	Secret     string // 当前 WS auth 不校验，可留空
	Passphrase string
}

// WithUserAuth 注入 user channel 凭证。
func WithUserAuth(auth UserAuth) FacadeOption {
	return func(f *Facade) {
		a := auth
		f.userAuth = &a
	}
}

// WithPingInterval 设置 PING 心跳间隔（默认 10s，与 asyncapi spec 对齐）。
func WithPingInterval(d time.Duration) FacadeOption {
	return func(f *Facade) {
		if d > 0 {
			f.pingInterval = d
		}
	}
}

// WithMaxBackoff 设置最大重连退避（默认 30s）。
func WithMaxBackoff(d time.Duration) FacadeOption {
	return func(f *Facade) {
		if d > 0 {
			f.maxBackoff = d
		}
	}
}

// withJitter 测试用 jitter 注入（返回 0 = 关闭抖动）。
func withJitter(fn func() time.Duration) FacadeOption {
	return func(f *Facade) { f.jitterFn = fn }
}

// NewFacade 构造一个 ws Facade。wsURL 形如 `ws://host:port`（不含 path，
// path 由 SubscribeBook/SubscribeOrders 各自拼接 `/ws/market` `/ws/user`）。
//
// 注意：当 wsURL 为空时仍返回非 nil Facade，但 SubscribeXxx 时会立即报错；
// 顶层 client.go 在未配置 wsURL 时也走这条路径，与 clob/gamma facade 保持一致。
func NewFacade(wsURL string, opts ...FacadeOption) (*Facade, error) {
	f := &Facade{
		wsURL:        wsURL,
		dialOpts:     &websocket.DialOptions{},
		pingInterval: 10 * time.Second,
		maxBackoff:   30 * time.Second,
		nowFn:        time.Now,
		jitterFn:     defaultJitter,
		logger:       obs.NopLogger{},
		metrics:      obs.NopMetrics{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(f)
		}
	}
	if f.dialFn == nil {
		f.dialFn = websocket.Dial
	}
	return f, nil
}

// NewStub 兼容 Phase 1-4 的占位入口；新代码请用 NewFacade。
//
// Deprecated: 仅为 Phase 1 兼容保留，将在 v0.2 移除。
func NewStub() *Facade {
	f, _ := NewFacade("")
	return f
}

// SubscribeBook 订阅市场频道（契约 §6）。
//
// 参数 tokenIDs 即上游 assets_ids（uint256 字符串数组）。返回的 channel：
//   - 首条事件：上游 `book` snapshot → BookUpdate{Type:SNAPSHOT}
//   - 后续：`price_change` → BookUpdate{Type:DELTA}
//   - 重连后：自动推一条 BookUpdate{Type:RESET, TokenID:""}（消费方应清缓存）
//   - sequence 跳变（timestamp 倒退）：推 RESET 后续费正常 DELTA 流
//
// channel 在 ctx 取消或 facade 永久放弃（罕见）后关闭。
func (f *Facade) SubscribeBook(ctx context.Context, tokenIDs []string) (<-chan BookUpdate, error) {
	if f.wsURL == "" {
		return nil, fmt.Errorf("%w: ws endpoint not configured", clob.ErrPrecondition)
	}
	if len(tokenIDs) == 0 {
		return nil, fmt.Errorf("%w: tokenIDs required", clob.ErrPrecondition)
	}
	out := make(chan BookUpdate, 64)
	go f.runBookLoop(ctx, joinPath(f.wsURL, "/ws/market"), tokenIDs, out)
	return out, nil
}

// SubscribeOrders 订阅用户频道（契约 §6）。
//
// 参数 userID（契约形参名）实际语义为 condition_id 列表过滤；上游 asyncapi-user.json
// 用户频道按 markets 数组过滤，单 condition_id 即一个市场。空字符串等价订阅所有 market。
//
// 必须 WithUserAuth 注入 apiKey + passphrase；缺失返回 ErrSign。
func (f *Facade) SubscribeOrders(ctx context.Context, userID string) (<-chan OrderUpdate, error) {
	if f.wsURL == "" {
		return nil, fmt.Errorf("%w: ws endpoint not configured", clob.ErrPrecondition)
	}
	if f.userAuth == nil {
		return nil, fmt.Errorf("%w: SubscribeOrders requires WithUserAuth", clob.ErrSign)
	}
	markets := []string{}
	if userID != "" {
		markets = []string{userID}
	}
	out := make(chan OrderUpdate, 64)
	go f.runOrderLoop(ctx, joinPath(f.wsURL, "/ws/user"), markets, out)
	return out, nil
}

// ---------- book loop ----------

func (f *Facade) runBookLoop(ctx context.Context, url string, tokenIDs []string, out chan<- BookUpdate) {
	defer close(out)

	g := newSeqGuard()
	attempt := 0    // 0 = 首次连接，>0 = 第 N 次重连
	disconnect := 0 // 断线计数；用于 sleepBackoff 的 attempt 入参
	for {
		if ctx.Err() != nil {
			return
		}

		// 重连前 sleep（首次连接 disconnect=0 跳过）
		if disconnect > 0 {
			if !f.sleepBackoff(ctx, disconnect-1) {
				return
			}
			f.metrics.IncCounter(obs.MetricWSReconnectsTotal, map[string]string{"channel": "market"})
			f.logger.Infow("ws reconnecting", "channel", "market", "attempt", disconnect)
		}

		conn, err := f.connect(ctx, url)
		if err != nil {
			f.logger.Warnw("ws connect failed",
				"channel", "market",
				"url", url,
				"attempt", disconnect,
				"error", err.Error(),
			)
			disconnect++
			continue
		}
		f.logger.Infow("ws connected", "channel", "market", "url", url)

		// 重连成功（首次连接 attempt==0 时不发 RESET）
		if attempt > 0 {
			select {
			case out <- BookUpdate{Type: UpdateReset, Time: f.nowFn()}:
			case <-ctx.Done():
				_ = conn.Close(websocket.StatusNormalClosure, "ctx cancel")
				return
			}
			g.reset()
		}

		// 发订阅
		sub := wireMarketSubscribe{
			AssetsIDs:   tokenIDs,
			Type:        "market",
			InitialDump: true,
			Level:       2,
		}
		if err := writeJSON(ctx, conn, sub); err != nil {
			_ = conn.Close(websocket.StatusInternalError, "subscribe failed")
			disconnect++
			continue
		}

		f.readBookFrames(ctx, conn, g, out)

		_ = conn.Close(websocket.StatusNormalClosure, "loop end")
		f.logger.Infow("ws disconnected", "channel", "market")
		if ctx.Err() != nil {
			return
		}
		attempt++
		disconnect = 1
	}
}

func (f *Facade) readBookFrames(ctx context.Context, conn *websocket.Conn, g *seqGuard, out chan<- BookUpdate) {
	pingCtx, cancelPing := context.WithCancel(ctx)
	defer cancelPing()
	go f.runPingLoop(pingCtx, conn)

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		// 上游可能发 PONG 文本帧，跳过
		if len(data) == 4 && string(data) == "PONG" {
			continue
		}

		updates := parseBookFrame(data)
		for _, up := range updates {
			// nonce guard
			if !g.accept(up.Sequence, frameHash(up)) {
				// timestamp 倒退或 hash 重复 → 推 RESET 让上层重建
				f.metrics.IncCounter(obs.MetricWSSeqJumpsTotal, map[string]string{"channel": "market"})
				f.logger.Warnw("ws sequence jump",
					"channel", "market",
					"token_id", up.TokenID,
					"sequence", up.Sequence,
				)
				select {
				case out <- BookUpdate{Type: UpdateReset, Time: f.nowFn()}:
					g.reset()
				case <-ctx.Done():
					return
				}
				continue
			}
			select {
			case out <- up:
			case <-ctx.Done():
				return
			}
		}
	}
}

// ---------- order loop ----------

func (f *Facade) runOrderLoop(ctx context.Context, url string, markets []string, out chan<- OrderUpdate) {
	defer close(out)

	disconnect := 0
	for {
		if ctx.Err() != nil {
			return
		}

		if disconnect > 0 {
			if !f.sleepBackoff(ctx, disconnect-1) {
				return
			}
			f.metrics.IncCounter(obs.MetricWSReconnectsTotal, map[string]string{"channel": "user"})
			f.logger.Infow("ws reconnecting", "channel", "user", "attempt", disconnect)
		}

		conn, err := f.connect(ctx, url)
		if err != nil {
			f.logger.Warnw("ws connect failed",
				"channel", "user",
				"url", url,
				"attempt", disconnect,
				"error", err.Error(),
			)
			disconnect++
			continue
		}
		f.logger.Infow("ws connected", "channel", "user", "url", url)

		sub := wireUserSubscribe{
			Auth: wireUserAuth{
				APIKey:     f.userAuth.APIKey,
				Secret:     f.userAuth.Secret,
				Passphrase: f.userAuth.Passphrase,
			},
			Type:    "user",
			Markets: markets,
		}
		if err := writeJSON(ctx, conn, sub); err != nil {
			_ = conn.Close(websocket.StatusInternalError, "subscribe failed")
			disconnect++
			continue
		}

		f.readOrderFrames(ctx, conn, out)

		_ = conn.Close(websocket.StatusNormalClosure, "loop end")
		f.logger.Infow("ws disconnected", "channel", "user")
		if ctx.Err() != nil {
			return
		}
		disconnect = 1
	}
}

func (f *Facade) readOrderFrames(ctx context.Context, conn *websocket.Conn, out chan<- OrderUpdate) {
	pingCtx, cancelPing := context.WithCancel(ctx)
	defer cancelPing()
	go f.runPingLoop(pingCtx, conn)

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if len(data) == 4 && string(data) == "PONG" {
			continue
		}

		var ev wireOrderEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			continue // 非 order 事件（trade 等）跳过
		}
		if ev.EventType != "order" {
			continue
		}

		up := OrderUpdate{
			OrderID: OrderID(ev.ID),
			Status:  mapOrderStatus(ev.Status),
			Time:    time.Unix(0, int64(ev.Timestamp)*int64(time.Millisecond)),
		}
		if d, err := decimal.NewFromString(ev.SizeMatched); err == nil {
			up.Filled = d
		}
		select {
		case out <- up:
		case <-ctx.Done():
			return
		}
	}
}

// ---------- 心跳 / 重连 / 工具 ----------

func (f *Facade) runPingLoop(ctx context.Context, conn *websocket.Conn) {
	if f.pingInterval <= 0 {
		return
	}
	t := time.NewTicker(f.pingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// 上游协议要求文本帧 "PING"
			writeCtx, cancel := context.WithTimeout(ctx, f.pingInterval)
			err := conn.Write(writeCtx, websocket.MessageText, []byte("PING"))
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func (f *Facade) connect(ctx context.Context, url string) (*websocket.Conn, error) {
	conn, _, err := f.dialFn(ctx, url, f.dialOpts)
	return conn, err
}

// sleepBackoff 按 1s,2s,4s,8s,16s,30s（封顶）+ 0-500ms jitter 等待。
// 返回 false 表示 ctx 取消，调用方应直接退出。
func (f *Facade) sleepBackoff(ctx context.Context, attempt int) bool {
	base := time.Duration(1<<minInt(attempt, 5)) * time.Second
	if base > f.maxBackoff {
		base = f.maxBackoff
	}
	d := base + f.jitterFn()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func defaultJitter() time.Duration {
	// 0-500ms；用 math/rand v1（lock-free 全局源也无所谓，jitter 不要求强随机）
	return time.Duration(rand.Intn(500)) * time.Millisecond
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// joinPath 拼 base + path；base 可带或不带尾 `/`。
func joinPath(base, path string) string {
	if base == "" {
		return path
	}
	// 简化：去掉 base 尾 `/`，path 必带前 `/`
	if base[len(base)-1] == '/' {
		base = base[:len(base)-1]
	}
	return base + path
}

func writeJSON(ctx context.Context, conn *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}

// parseBookFrame 把上游一帧 JSON 解析成 0..N 个 BookUpdate。
//
// 上游可能发：
//   - 单个 object：{"event_type":"book"/"price_change", ...}
//   - 数组（同一帧多事件）：[{...},{...}]
//   - 其他事件（last_trade_price / tick_size_change / best_bid_ask）：当前忽略
//
// 解析失败的帧返回空切片（不向上抛错；上层 readLoop 已通过 conn.Read err 感知断线）。
func parseBookFrame(data []byte) []BookUpdate {
	// 试数组
	if len(data) > 0 && data[0] == '[' {
		var arr []json.RawMessage
		if err := json.Unmarshal(data, &arr); err != nil {
			return nil
		}
		out := make([]BookUpdate, 0, len(arr))
		for _, raw := range arr {
			if u, ok := parseBookSingle(raw); ok {
				out = append(out, u...)
			}
		}
		return out
	}
	if u, ok := parseBookSingle(data); ok {
		return u
	}
	return nil
}

func parseBookSingle(data []byte) ([]BookUpdate, bool) {
	// 先嗅 event_type
	var hdr struct {
		EventType string `json:"event_type"`
	}
	if err := json.Unmarshal(data, &hdr); err != nil {
		return nil, false
	}
	switch hdr.EventType {
	case "book":
		var snap wireBookSnapshot
		if err := json.Unmarshal(data, &snap); err != nil {
			return nil, false
		}
		up := BookUpdate{
			TokenID:  snap.AssetID,
			Type:     UpdateSnapshot,
			Sequence: int64(snap.Timestamp),
			Time:     time.Unix(0, int64(snap.Timestamp)*int64(time.Millisecond)),
		}
		for _, lv := range snap.Bids {
			up.Bids = append(up.Bids, lvlFromWire(lv))
		}
		for _, lv := range snap.Asks {
			up.Asks = append(up.Asks, lvlFromWire(lv))
		}
		// 把 hash 编码进 Sequence 不合适；在调用处的 nonce guard 用专门方法读
		// （见 frameHash）。这里 SNAPSHOT 不带 hash，guard 接受。
		_ = snap.Hash
		return []BookUpdate{up}, true
	case "price_change":
		var pc wirePriceChange
		if err := json.Unmarshal(data, &pc); err != nil {
			return nil, false
		}
		// 一帧 price_change 可能携带多 token 多档位变更；按 asset_id 分组
		grouped := map[string]*BookUpdate{}
		ts := int64(pc.Timestamp)
		t := time.Unix(0, ts*int64(time.Millisecond))
		for _, ch := range pc.PriceChanges {
			u, ok := grouped[ch.AssetID]
			if !ok {
				u = &BookUpdate{
					TokenID:  ch.AssetID,
					Type:     UpdateDelta,
					Sequence: ts,
					Time:     t,
				}
				grouped[ch.AssetID] = u
			}
			lvl := clob.Level{}
			if d, err := decimal.NewFromString(ch.Price); err == nil {
				lvl.Price = d
			}
			if d, err := decimal.NewFromString(ch.Size); err == nil {
				lvl.Size = d
			}
			switch ch.Side {
			case "BUY":
				u.Bids = append(u.Bids, lvl)
			case "SELL":
				u.Asks = append(u.Asks, lvl)
			}
		}
		out := make([]BookUpdate, 0, len(grouped))
		for _, u := range grouped {
			out = append(out, *u)
		}
		return out, true
	default:
		// last_trade_price / tick_size_change / best_bid_ask / new_market /
		// market_resolved / pong text 等当前不暴露
		return nil, true
	}
}

// frameHash 从 BookUpdate 派生一个稳定字符串供 nonce guard 去重。
// 简化：用 sequence + 各档 price/size 串接；hash 完全相同的连续帧视为重复。
func frameHash(u BookUpdate) string {
	h := strconv.FormatInt(u.Sequence, 10) + "|" + u.TokenID + "|" + string(u.Type)
	for _, b := range u.Bids {
		h += "|b" + b.Price.String() + ":" + b.Size.String()
	}
	for _, a := range u.Asks {
		h += "|a" + a.Price.String() + ":" + a.Size.String()
	}
	return h
}

func lvlFromWire(w wireOrderLvl) clob.Level {
	out := clob.Level{}
	if d, err := decimal.NewFromString(w.Price); err == nil {
		out.Price = d
	}
	if d, err := decimal.NewFromString(w.Size); err == nil {
		out.Size = d
	}
	return out
}

func mapOrderStatus(s string) clob.SdkOrderStatus {
	switch s {
	case "ORDER_STATUS_LIVE":
		return clob.OrderStatusOpen
	case "ORDER_STATUS_MATCHED":
		return clob.OrderStatusFilled
	case "ORDER_STATUS_CANCELED", "ORDER_STATUS_CANCELED_MARKET_RESOLVED":
		return clob.OrderStatusCancelled
	case "ORDER_STATUS_INVALID":
		return clob.OrderStatusRejected
	default:
		return clob.SdkOrderStatus(s)
	}
}

// ---------- seq guard ----------

// seqGuard 是 nonce / sequence guard：
//   - 拒绝 sequence 严格倒退（< lastSeq）的帧 → 触发 RESET
//   - 拒绝完全重复的 hash（== lastHash） → 触发 RESET
//   - sequence 相等但 hash 不同：放行（同 ms 多事件合法）
//
// 不做"跳号检测"严格意义（spec 没有连续 sequence number），用 timestamp 单调
// 性近似；这与契约 §6 "Sequence 跳跃 → 推 RESET" 的语义一致。
type seqGuard struct {
	mu       sync.Mutex
	lastSeq  int64
	lastHash string
}

func newSeqGuard() *seqGuard { return &seqGuard{} }

func (g *seqGuard) accept(seq int64, hash string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if seq == 0 && hash == "" {
		// RESET / 缺 timestamp 的事件直接放行
		return true
	}
	if g.lastSeq == 0 && g.lastHash == "" {
		g.lastSeq = seq
		g.lastHash = hash
		return true
	}
	if seq < g.lastSeq {
		return false // 时光倒流
	}
	if seq == g.lastSeq && hash == g.lastHash {
		return false // 完全重复
	}
	g.lastSeq = seq
	g.lastHash = hash
	return true
}

func (g *seqGuard) reset() {
	g.mu.Lock()
	g.lastSeq = 0
	g.lastHash = ""
	g.mu.Unlock()
}

