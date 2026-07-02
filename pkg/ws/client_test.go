package ws

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/shopspring/decimal"

	"github.com/chainupcloud/pm-sdk-go/pkg/clob"
)

// ---------- mock server framework ----------

// mockServerOpts 控制 mock 行为：每次新连接调用一次 onConn 回调。
type mockServerOpts struct {
	onConn func(ctx context.Context, conn *websocket.Conn, reqs <-chan json.RawMessage) // 由 caller 关闭 conn
}

// startMockWS 起一个 httptest server 接 ws upgrade；调用方负责写测试场景。
func startMockWS(t *testing.T, opts mockServerOpts) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		// 把 client 发来的订阅 / PING 消息转发到 reqs channel，供 onConn 决策
		reqs := make(chan json.RawMessage, 8)
		go func() {
			defer close(reqs)
			for {
				_, data, err := c.Read(ctx)
				if err != nil {
					return
				}
				// 把 PING 文本帧也包进 raw（caller 自己识别）
				select {
				case reqs <- json.RawMessage(data):
				case <-ctx.Done():
					return
				}
			}
		}()

		if opts.onConn != nil {
			opts.onConn(ctx, c, reqs)
		}
		_ = c.Close(websocket.StatusNormalClosure, "test done")
	}))
	// 把 http://... 转 ws://...
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	return srv, wsURL
}

// writeMsg helper.
func writeMsg(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// noJitter 关闭重连抖动，让测试时序确定。
func noJitter() time.Duration { return 0 }

// ---------- happy path ----------

func TestSubscribeBook_HappyPath(t *testing.T) {
	t.Parallel()

	const tokenID = "token-A"

	srv, wsURL := startMockWS(t, mockServerOpts{
		onConn: func(ctx context.Context, c *websocket.Conn, reqs <-chan json.RawMessage) {
			// 等订阅
			select {
			case raw := <-reqs:
				var sub wireMarketSubscribe
				if err := json.Unmarshal(raw, &sub); err != nil {
					t.Errorf("decode subscribe: %v", err)
					return
				}
				if len(sub.AssetsIDs) != 1 || sub.AssetsIDs[0] != tokenID {
					t.Errorf("subscribe assets_ids = %v, want [%s]", sub.AssetsIDs, tokenID)
				}
			case <-ctx.Done():
				return
			}

			// 发 SNAPSHOT
			writeMsg(t, c, map[string]any{
				"event_type": "book",
				"asset_id":   tokenID,
				"market":     "market-1",
				"bids":       []map[string]string{{"price": "0.5", "size": "100"}},
				"asks":       []map[string]string{{"price": "0.6", "size": "200"}},
				"timestamp":  1000,
				"hash":       "h1",
			})
			// 发 DELTA
			writeMsg(t, c, map[string]any{
				"event_type": "price_change",
				"market":     "market-1",
				"price_changes": []map[string]any{
					{"asset_id": tokenID, "price": "0.51", "size": "50", "side": "BUY", "hash": "h2"},
				},
				"timestamp": 1100,
			})
			<-ctx.Done()
		},
	})
	defer srv.Close()

	f, err := NewFacade(wsURL, withJitter(noJitter))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := f.SubscribeBook(ctx, []string{tokenID})
	if err != nil {
		t.Fatal(err)
	}

	got1 := <-ch
	if got1.Type != UpdateSnapshot {
		t.Errorf("first event type = %s, want SNAPSHOT", got1.Type)
	}
	if got1.TokenID != tokenID {
		t.Errorf("token = %s", got1.TokenID)
	}
	if len(got1.Bids) != 1 || !got1.Bids[0].Price.Equal(decimal.RequireFromString("0.5")) {
		t.Errorf("bids = %+v", got1.Bids)
	}

	got2 := <-ch
	if got2.Type != UpdateDelta {
		t.Errorf("second event type = %s, want DELTA", got2.Type)
	}
	if len(got2.Bids) != 1 {
		t.Errorf("delta bids = %+v", got2.Bids)
	}
}

// ---------- reconnect → RESET ----------

func TestSubscribeBook_ReconnectEmitsReset(t *testing.T) {
	t.Parallel()

	const tokenID = "token-R"
	var connCount atomic.Int32

	srv, wsURL := startMockWS(t, mockServerOpts{
		onConn: func(ctx context.Context, c *websocket.Conn, reqs <-chan json.RawMessage) {
			n := connCount.Add(1)
			// 两次都收 subscribe
			select {
			case <-reqs:
			case <-ctx.Done():
				return
			}
			if n == 1 {
				// 首次连接：发 SNAPSHOT 后立即关闭
				writeMsg(t, c, map[string]any{
					"event_type": "book",
					"asset_id":   tokenID,
					"timestamp":  2000,
					"hash":       "first",
				})
				_ = c.Close(websocket.StatusGoingAway, "drop")
				return
			}
			// 第二次连接：发 SNAPSHOT 然后挂着等 ctx
			writeMsg(t, c, map[string]any{
				"event_type": "book",
				"asset_id":   tokenID,
				"timestamp":  3000,
				"hash":       "second",
			})
			<-ctx.Done()
		},
	})
	defer srv.Close()

	// 用极短的 maxBackoff 让重连快
	f, err := NewFacade(wsURL,
		WithMaxBackoff(50*time.Millisecond),
		withJitter(noJitter),
	)
	if err != nil {
		t.Fatal(err)
	}
	// hack：把 base 退避也压短 —— 通过把 maxBackoff 压到 50ms，1s 也会被截断到 50ms
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := f.SubscribeBook(ctx, []string{tokenID})
	if err != nil {
		t.Fatal(err)
	}

	// 期待序列：SNAPSHOT(first) → RESET → SNAPSHOT(second)
	want := []UpdateType{UpdateSnapshot, UpdateReset, UpdateSnapshot}
	for i, w := range want {
		select {
		case got := <-ch:
			if got.Type != w {
				t.Errorf("event %d type = %s, want %s", i, got.Type, w)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timeout waiting event %d (want %s)", i, w)
		}
	}
}

// ---------- sequence jump → RESET ----------

func TestSubscribeBook_SequenceJumpEmitsReset(t *testing.T) {
	t.Parallel()

	const tokenID = "token-S"

	srv, wsURL := startMockWS(t, mockServerOpts{
		onConn: func(ctx context.Context, c *websocket.Conn, reqs <-chan json.RawMessage) {
			select {
			case <-reqs:
			case <-ctx.Done():
				return
			}
			// 发 SNAPSHOT ts=5000
			writeMsg(t, c, map[string]any{
				"event_type": "book", "asset_id": tokenID,
				"timestamp": 5000, "hash": "h1",
			})
			// 发 DELTA ts=6000（正常）
			writeMsg(t, c, map[string]any{
				"event_type": "price_change",
				"price_changes": []map[string]any{
					{"asset_id": tokenID, "price": "0.5", "size": "10", "side": "BUY", "hash": "h2"},
				},
				"timestamp": 6000,
			})
			// 发倒退 DELTA ts=4000 → 触发 RESET
			writeMsg(t, c, map[string]any{
				"event_type": "price_change",
				"price_changes": []map[string]any{
					{"asset_id": tokenID, "price": "0.4", "size": "20", "side": "BUY", "hash": "h3"},
				},
				"timestamp": 4000,
			})
			<-ctx.Done()
		},
	})
	defer srv.Close()

	f, err := NewFacade(wsURL, withJitter(noJitter))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := f.SubscribeBook(ctx, []string{tokenID})
	if err != nil {
		t.Fatal(err)
	}

	// SNAPSHOT
	if got := <-ch; got.Type != UpdateSnapshot {
		t.Errorf("ev0 type = %s", got.Type)
	}
	// DELTA ts=6000
	if got := <-ch; got.Type != UpdateDelta {
		t.Errorf("ev1 type = %s", got.Type)
	}
	// 第三帧因 ts 倒退被拒 → RESET
	select {
	case got := <-ch:
		if got.Type != UpdateReset {
			t.Errorf("ev2 type = %s, want RESET", got.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting RESET after seq jump")
	}
}

// ---------- ctx cancel cleanly closes channel ----------

func TestSubscribeBook_CtxCancelClosesChannel(t *testing.T) {
	t.Parallel()

	srv, wsURL := startMockWS(t, mockServerOpts{
		onConn: func(ctx context.Context, c *websocket.Conn, reqs <-chan json.RawMessage) {
			select {
			case <-reqs:
			case <-ctx.Done():
				return
			}
			<-ctx.Done()
		},
	})
	defer srv.Close()

	f, err := NewFacade(wsURL, withJitter(noJitter))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := f.SubscribeBook(ctx, []string{"x"})
	if err != nil {
		t.Fatal(err)
	}
	// 等连接稳定
	time.Sleep(100 * time.Millisecond)
	cancel()

	// channel 必须 close（非阻塞 read 拿到零值 ok=false）
	select {
	case _, ok := <-ch:
		if ok {
			// 第一条可能还没读到 —— 但既然 cancel 了，第二次 read 必为 closed
			select {
			case _, ok2 := <-ch:
				if ok2 {
					t.Fatal("channel should close after ctx cancel")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("channel did not close in time")
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel did not close in time")
	}
}

// ---------- subscribe orders ----------

func TestSubscribeOrders_HappyPath(t *testing.T) {
	t.Parallel()

	srv, wsURL := startMockWS(t, mockServerOpts{
		onConn: func(ctx context.Context, c *websocket.Conn, reqs <-chan json.RawMessage) {
			select {
			case raw := <-reqs:
				var sub wireUserSubscribe
				if err := json.Unmarshal(raw, &sub); err != nil {
					t.Errorf("decode user sub: %v", err)
					return
				}
				if sub.Auth.APIKey != "k" || sub.Auth.Passphrase != "p" {
					t.Errorf("auth mismatch: %+v", sub.Auth)
				}
			case <-ctx.Done():
				return
			}

			writeMsg(t, c, map[string]any{
				"event_type":   "order",
				"type":         "PLACEMENT",
				"id":           "0xabc",
				"status":       "ORDER_STATUS_LIVE",
				"size_matched": "10",
				"timestamp":    7000,
			})
			<-ctx.Done()
		},
	})
	defer srv.Close()

	f, err := NewFacade(wsURL,
		WithUserAuth(UserAuth{APIKey: "k", Passphrase: "p"}),
		withJitter(noJitter),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ch, err := f.SubscribeOrders(ctx, "market-1")
	if err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-ch:
		if got.OrderID != "0xabc" {
			t.Errorf("order id = %s", got.OrderID)
		}
		if got.Status != clob.OrderStatusOpen {
			t.Errorf("status = %s", got.Status)
		}
		if !got.Filled.Equal(decimal.NewFromInt(10)) {
			t.Errorf("filled = %s", got.Filled)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting order event")
	}
}

func TestSubscribeOrders_RequiresAuth(t *testing.T) {
	t.Parallel()

	f, err := NewFacade("ws://example.invalid", withJitter(noJitter))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.SubscribeOrders(context.Background(), "m")
	if !errors.Is(err, clob.ErrSign) {
		t.Errorf("err = %v, want ErrSign", err)
	}
}

// ---------- input validation ----------

func TestSubscribeBook_EmptyURL(t *testing.T) {
	t.Parallel()
	f, _ := NewFacade("")
	_, err := f.SubscribeBook(context.Background(), []string{"x"})
	if !errors.Is(err, clob.ErrPrecondition) {
		t.Errorf("err = %v, want ErrPrecondition", err)
	}
}

func TestSubscribeBook_EmptyTokenIDs(t *testing.T) {
	t.Parallel()
	f, _ := NewFacade("ws://localhost:0", withJitter(noJitter))
	_, err := f.SubscribeBook(context.Background(), nil)
	if !errors.Is(err, clob.ErrPrecondition) {
		t.Errorf("err = %v, want ErrPrecondition", err)
	}
}

// ---------- seqGuard unit ----------

func TestSeqGuard(t *testing.T) {
	t.Parallel()
	g := newSeqGuard()
	if !g.accept(100, "h1") {
		t.Fatal("first accept")
	}
	if !g.accept(200, "h2") {
		t.Fatal("monotonic accept")
	}
	if g.accept(150, "h3") {
		t.Fatal("must reject backward")
	}
	if g.accept(200, "h2") {
		t.Fatal("must reject duplicate")
	}
	// 同 ts 不同 hash 放行
	if !g.accept(200, "h2-different") {
		t.Fatal("same ts diff hash should accept")
	}
	g.reset()
	if !g.accept(50, "x") {
		t.Fatal("after reset accept")
	}
}

// ---------- nonce guard via SubscribeBook (duplicate hash → RESET) ----------

func TestSubscribeBook_DuplicateFrameEmitsReset(t *testing.T) {
	t.Parallel()
	const tokenID = "dup-token"

	srv, wsURL := startMockWS(t, mockServerOpts{
		onConn: func(ctx context.Context, c *websocket.Conn, reqs <-chan json.RawMessage) {
			select {
			case <-reqs:
			case <-ctx.Done():
				return
			}
			// 同 timestamp + 同 payload 发两次 → 第二次应触发 RESET
			snap := map[string]any{
				"event_type": "price_change",
				"price_changes": []map[string]any{
					{"asset_id": tokenID, "price": "0.5", "size": "10", "side": "BUY", "hash": "same"},
				},
				"timestamp": 9000,
			}
			writeMsg(t, c, snap)
			writeMsg(t, c, snap)
			<-ctx.Done()
		},
	})
	defer srv.Close()

	f, err := NewFacade(wsURL, withJitter(noJitter))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ch, err := f.SubscribeBook(ctx, []string{tokenID})
	if err != nil {
		t.Fatal(err)
	}

	// 第一帧 DELTA 通过
	if got := <-ch; got.Type != UpdateDelta {
		t.Errorf("ev0 = %s, want DELTA", got.Type)
	}
	// 第二帧重复 → RESET
	select {
	case got := <-ch:
		if got.Type != UpdateReset {
			t.Errorf("ev1 = %s, want RESET", got.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting RESET on duplicate frame")
	}
}

// ---------- sleepBackoff ----------

func TestSleepBackoff_RespectsCtx(t *testing.T) {
	t.Parallel()
	f, _ := NewFacade("ws://x", WithMaxBackoff(time.Second), withJitter(noJitter))
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消
	if f.sleepBackoff(ctx, 0) {
		t.Error("sleepBackoff should return false on cancelled ctx")
	}
}

func TestSleepBackoff_CapAtMax(t *testing.T) {
	t.Parallel()
	// max=10ms，attempt=10 → 1<<5=32s base 应被截断到 10ms
	f, _ := NewFacade("ws://x", WithMaxBackoff(10*time.Millisecond), withJitter(noJitter))
	start := time.Now()
	ok := f.sleepBackoff(context.Background(), 10)
	elapsed := time.Since(start)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if elapsed > 50*time.Millisecond {
		t.Errorf("sleep too long: %v (cap should clamp)", elapsed)
	}
}

// ---------- joinPath ----------

func TestJoinPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		base, path, want string
	}{
		{"ws://h", "/ws/market", "ws://h/ws/market"},
		{"ws://h/", "/ws/market", "ws://h/ws/market"},
		{"", "/ws/x", "/ws/x"},
	}
	for _, c := range cases {
		if got := joinPath(c.base, c.path); got != c.want {
			t.Errorf("joinPath(%q,%q)=%q want %q", c.base, c.path, got, c.want)
		}
	}
}

// ---------- parseBookFrame: array & ignored events ----------

func TestParseBookFrame_Array(t *testing.T) {
	t.Parallel()
	raw := []byte(`[{"event_type":"book","asset_id":"a","timestamp":1,"hash":"h"},{"event_type":"last_trade_price"}]`)
	got := parseBookFrame(raw)
	if len(got) != 1 {
		t.Fatalf("got %d updates, want 1 (last_trade_price ignored)", len(got))
	}
	if got[0].Type != UpdateSnapshot {
		t.Errorf("type=%s", got[0].Type)
	}
}

func TestParseBookFrame_InvalidJSON(t *testing.T) {
	t.Parallel()
	if got := parseBookFrame([]byte("not json")); got != nil {
		t.Errorf("invalid json should return nil, got %v", got)
	}
	if got := parseBookFrame([]byte(`[bad`)); got != nil {
		t.Errorf("bad array should return nil, got %v", got)
	}
}

// ---------- timestamp string form ----------

func TestWireTimestampMs_StringForm(t *testing.T) {
	t.Parallel()
	var ts wireTimestampMs
	if err := ts.UnmarshalJSON([]byte(`"12345"`)); err != nil {
		t.Fatal(err)
	}
	if ts != 12345 {
		t.Errorf("ts=%d", ts)
	}
	// number form
	if err := ts.UnmarshalJSON([]byte(`67890`)); err != nil {
		t.Fatal(err)
	}
	if ts != 67890 {
		t.Errorf("ts=%d", ts)
	}
	// null
	ts = 100
	if err := ts.UnmarshalJSON([]byte(`null`)); err != nil {
		t.Fatal(err)
	}
	if ts != 100 {
		t.Errorf("null should leave value untouched, got %d", ts)
	}
	// invalid
	if err := ts.UnmarshalJSON([]byte(`"abc"`)); err == nil {
		t.Error("expected error on invalid string")
	}
}

// ---------- mapOrderStatus all branches ----------

func TestMapOrderStatus(t *testing.T) {
	t.Parallel()
	cases := map[string]clob.SdkOrderStatus{
		"ORDER_STATUS_LIVE":                     clob.OrderStatusOpen,
		"ORDER_STATUS_MATCHED":                  clob.OrderStatusFilled,
		"ORDER_STATUS_CANCELED":                 clob.OrderStatusCancelled,
		"ORDER_STATUS_CANCELED_MARKET_RESOLVED": clob.OrderStatusCancelled,
		"ORDER_STATUS_INVALID":                  clob.OrderStatusRejected,
		"WHATEVER":                              clob.SdkOrderStatus("WHATEVER"),
	}
	for in, want := range cases {
		if got := mapOrderStatus(in); got != want {
			t.Errorf("mapOrderStatus(%s)=%s want %s", in, got, want)
		}
	}
}

// ---------- NewStub deprecated entry ----------

func TestNewStub(t *testing.T) {
	t.Parallel()
	if NewStub() == nil {
		t.Fatal("NewStub returned nil")
	}
}

// ---------- goroutine leak smoke ----------

func TestSubscribeBook_NoGoroutineLeak(t *testing.T) {
	t.Parallel()

	srv, wsURL := startMockWS(t, mockServerOpts{
		onConn: func(ctx context.Context, c *websocket.Conn, reqs <-chan json.RawMessage) {
			select {
			case <-reqs:
			case <-ctx.Done():
				return
			}
			<-ctx.Done()
		},
	})
	defer srv.Close()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, _ := NewFacade(wsURL, withJitter(noJitter))
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			ch, err := f.SubscribeBook(ctx, []string{"x"})
			if err != nil {
				return
			}
			// drain 直到 close
			for range ch {
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("goroutine leak: SubscribeBook did not exit after ctx cancel")
	}
}

// ---------- subscribe trades ----------

// tradeEnvelope 构造 clob-service 实际下发的 envelope 形态 trade 帧
// （payload 嵌在 data 对象里，见 wsservice.UserEvent）。
func tradeEnvelope(data map[string]any) map[string]any {
	return map[string]any{
		"event_type":   "trade",
		"owner":        "owner-uuid",
		"condition_id": "0xcond",
		"data":         data,
	}
}

// ① match 侧（撮合瞬间）短串 status 帧解析。
func TestSubscribeTrades_MatchFrame(t *testing.T) {
	t.Parallel()

	srv, wsURL := startMockWS(t, mockServerOpts{
		onConn: func(ctx context.Context, c *websocket.Conn, reqs <-chan json.RawMessage) {
			select {
			case raw := <-reqs:
				var sub wireUserSubscribe
				if err := json.Unmarshal(raw, &sub); err != nil {
					t.Errorf("decode user sub: %v", err)
					return
				}
				if sub.Auth.APIKey != "k" || sub.Auth.Passphrase != "p" {
					t.Errorf("auth mismatch: %+v", sub.Auth)
				}
			case <-ctx.Done():
				return
			}

			writeMsg(t, c, tradeEnvelope(map[string]any{
				"id":         "123",
				"status":     "MATCHED",
				"asset_id":   "tok-1",
				"side":       "BUY",
				"price":      "0.55",
				"size":       "100",
				"match_type": "COMPLEMENTARY",
				"order_id":   "0xabc",
			}))
			<-ctx.Done()
		},
	})
	defer srv.Close()

	f, err := NewFacade(wsURL,
		WithUserAuth(UserAuth{APIKey: "k", Passphrase: "p"}),
		withJitter(noJitter),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ch, err := f.SubscribeTrades(ctx, "market-1")
	if err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-ch:
		if got.TradeID != "123" {
			t.Errorf("trade id = %s", got.TradeID)
		}
		if got.Status != "MATCHED" {
			t.Errorf("status = %s", got.Status)
		}
		if got.AssetID != "tok-1" {
			t.Errorf("asset id = %s", got.AssetID)
		}
		if got.Side != clob.SideBuy {
			t.Errorf("side = %s", got.Side)
		}
		if !got.Price.Equal(decimal.RequireFromString("0.55")) {
			t.Errorf("price = %s", got.Price)
		}
		if !got.Size.Equal(decimal.NewFromInt(100)) {
			t.Errorf("size = %s", got.Size)
		}
		if got.OrderID != "0xabc" {
			t.Errorf("order id = %s", got.OrderID)
		}
		if got.TxHash != "" || got.Error != "" {
			t.Errorf("match frame should not carry tx_hash/error: %+v", got)
		}
		if got.Time.IsZero() {
			t.Error("time should be set")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting trade event")
	}
}

// ② settle 侧长串 status（CONFIRMED / FAILED）帧解析，含 tx_hash / error。
func TestSubscribeTrades_SettleConfirmedAndFailed(t *testing.T) {
	t.Parallel()

	srv, wsURL := startMockWS(t, mockServerOpts{
		onConn: func(ctx context.Context, c *websocket.Conn, reqs <-chan json.RawMessage) {
			select {
			case <-reqs:
			case <-ctx.Done():
				return
			}
			// settle 侧：size/fee 为 ×10^6 最小单位、price 为 DECIMAL 字符串
			writeMsg(t, c, tradeEnvelope(map[string]any{
				"id":       "456",
				"status":   "TRADE_STATUS_CONFIRMED",
				"asset_id": "tok-2",
				"side":     "SELL",
				"size":     "100000000",
				"price":    "0.550000",
				"fee":      "70000",
				"tx_hash":  "0xdeadbeef",
			}))
			writeMsg(t, c, tradeEnvelope(map[string]any{
				"id":       "789",
				"status":   "TRADE_STATUS_FAILED",
				"asset_id": "tok-2",
				"side":     "SELL",
				"size":     "100000000",
				"price":    "0.550000",
				"fee":      "0",
				"error":    "tx reverted",
			}))
			<-ctx.Done()
		},
	})
	defer srv.Close()

	f, err := NewFacade(wsURL,
		WithUserAuth(UserAuth{APIKey: "k", Passphrase: "p"}),
		withJitter(noJitter),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ch, err := f.SubscribeTrades(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	// CONFIRMED
	select {
	case got := <-ch:
		if got.TradeID != "456" {
			t.Errorf("trade id = %s", got.TradeID)
		}
		if got.Status != "TRADE_STATUS_CONFIRMED" {
			t.Errorf("status = %s", got.Status)
		}
		if got.TxHash != "0xdeadbeef" {
			t.Errorf("tx_hash = %s", got.TxHash)
		}
		if !got.Price.Equal(decimal.RequireFromString("0.55")) {
			t.Errorf("price = %s", got.Price)
		}
		if !got.Size.Equal(decimal.NewFromInt(100000000)) {
			t.Errorf("size = %s (settle 侧最小单位应原样透传)", got.Size)
		}
		if !got.Fee.Equal(decimal.NewFromInt(70000)) {
			t.Errorf("fee = %s", got.Fee)
		}
		if got.OrderID != "" {
			t.Errorf("settle frame has no order_id, got %s", got.OrderID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting CONFIRMED event")
	}

	// FAILED
	select {
	case got := <-ch:
		if got.Status != "TRADE_STATUS_FAILED" {
			t.Errorf("status = %s", got.Status)
		}
		if got.Error != "tx reverted" {
			t.Errorf("error = %s", got.Error)
		}
		if got.TxHash != "" {
			t.Errorf("FAILED frame should not carry tx_hash, got %s", got.TxHash)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting FAILED event")
	}
}

// parseTradeFrame 单元：平铺形态（asyncapi 文档 / Polymarket 形态），数值字段
// 可能是 number 而非 string —— wire 结构须两种都容忍。
func TestParseTradeFrame_FlatNumberForm(t *testing.T) {
	t.Parallel()

	frame := []byte(`{"event_type":"trade","id":"9","status":"TRADE_STATUS_CONFIRMED","asset_id":"a","side":"SELL","price":0.55,"size":100,"fee":7,"tx_hash":"0x1"}`)
	up, ok := parseTradeFrame(frame, nil)
	if !ok {
		t.Fatal("parse failed")
	}
	if up.TradeID != "9" || up.Status != "TRADE_STATUS_CONFIRMED" {
		t.Errorf("id/status = %s/%s", up.TradeID, up.Status)
	}
	if !up.Price.Equal(decimal.RequireFromString("0.55")) {
		t.Errorf("price = %s", up.Price)
	}
	if !up.Size.Equal(decimal.NewFromInt(100)) {
		t.Errorf("size = %s", up.Size)
	}
	if !up.Fee.Equal(decimal.NewFromInt(7)) {
		t.Errorf("fee = %s", up.Fee)
	}
	if up.TxHash != "0x1" {
		t.Errorf("tx_hash = %s", up.TxHash)
	}
}

func TestParseTradeFrame_InvalidPayload(t *testing.T) {
	t.Parallel()
	if _, ok := parseTradeFrame([]byte(`not json`), nil); ok {
		t.Error("invalid frame should not parse")
	}
	// envelope data 非 object（如 null / string）时回退整帧平铺解析
	up, ok := parseTradeFrame([]byte(`{"event_type":"trade","id":"x","data":null}`), json.RawMessage(`null`))
	if !ok || up.TradeID != "x" {
		t.Errorf("fallback flat parse: ok=%v up=%+v", ok, up)
	}
}

// ③ order 事件路径回归：trade 帧混在 order 帧中间，order channel 只收 order 事件。
func TestSubscribeOrders_TradeFramesDoNotAffectOrderPath(t *testing.T) {
	t.Parallel()

	srv, wsURL := startMockWS(t, mockServerOpts{
		onConn: func(ctx context.Context, c *websocket.Conn, reqs <-chan json.RawMessage) {
			select {
			case <-reqs:
			case <-ctx.Done():
				return
			}
			// 先发 trade envelope 帧（旧版直接丢弃；新版不得投递到 order channel）
			writeMsg(t, c, tradeEnvelope(map[string]any{
				"id": "1", "status": "MATCHED", "asset_id": "tok", "side": "BUY",
				"price": "0.5", "size": "10", "order_id": "0xignored",
			}))
			// 再发 order 帧（与现有 TestSubscribeOrders_HappyPath 同形态）
			writeMsg(t, c, map[string]any{
				"event_type":   "order",
				"type":         "PLACEMENT",
				"id":           "0xorder",
				"status":       "ORDER_STATUS_LIVE",
				"size_matched": "10",
				"timestamp":    7000,
			})
			<-ctx.Done()
		},
	})
	defer srv.Close()

	f, err := NewFacade(wsURL,
		WithUserAuth(UserAuth{APIKey: "k", Passphrase: "p"}),
		withJitter(noJitter),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ch, err := f.SubscribeOrders(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-ch:
		if got.OrderID != "0xorder" {
			t.Errorf("order channel got unexpected event: %+v", got)
		}
		if got.Status != clob.OrderStatusOpen {
			t.Errorf("status = %s", got.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting order event")
	}
}

// 同一 userID 下 SubscribeOrders + SubscribeTrades 共用一条 /ws/user 连接
// （上游同连接双投递，不开第二条连接）。
func TestSubscribeOrdersAndTrades_ShareOneConnection(t *testing.T) {
	t.Parallel()

	var connCount atomic.Int32
	srv, wsURL := startMockWS(t, mockServerOpts{
		onConn: func(ctx context.Context, c *websocket.Conn, reqs <-chan json.RawMessage) {
			connCount.Add(1)
			select {
			case <-reqs:
			case <-ctx.Done():
				return
			}
			// 等第二个订阅方 attach（同连接无第二条 subscribe 帧可等）
			time.Sleep(300 * time.Millisecond)
			writeMsg(t, c, map[string]any{
				"event_type": "order", "id": "0xshared",
				"status": "ORDER_STATUS_LIVE", "size_matched": "0", "timestamp": 1,
			})
			writeMsg(t, c, tradeEnvelope(map[string]any{
				"id": "42", "status": "TRADE_STATUS_CONFIRMED", "asset_id": "tok",
				"side": "BUY", "price": "0.5", "size": "1000000", "tx_hash": "0xtx",
			}))
			<-ctx.Done()
		},
	})
	defer srv.Close()

	f, err := NewFacade(wsURL,
		WithUserAuth(UserAuth{APIKey: "k", Passphrase: "p"}),
		withJitter(noJitter),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	orderCh, err := f.SubscribeOrders(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	tradeCh, err := f.SubscribeTrades(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-orderCh:
		if got.OrderID != "0xshared" {
			t.Errorf("order id = %s", got.OrderID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting order event")
	}
	select {
	case got := <-tradeCh:
		if got.TradeID != "42" {
			t.Errorf("trade id = %s", got.TradeID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting trade event")
	}
	if n := connCount.Load(); n != 1 {
		t.Errorf("connections = %d, want 1 (orders + trades 应共用一条连接)", n)
	}
}

// ④ trade channel 满时非阻塞丢弃，不拖住同一连接上的 order 投递。
func TestSubscribeTrades_ChannelFullDoesNotBlockOrders(t *testing.T) {
	t.Parallel()

	const total = 70 // > buffer 64
	srv, wsURL := startMockWS(t, mockServerOpts{
		onConn: func(ctx context.Context, c *websocket.Conn, reqs <-chan json.RawMessage) {
			select {
			case <-reqs:
			case <-ctx.Done():
				return
			}
			time.Sleep(300 * time.Millisecond) // 等两个订阅方都 attach
			for i := 1; i <= total; i++ {
				writeMsg(t, c, tradeEnvelope(map[string]any{
					"id": strconv.Itoa(i), "status": "MATCHED", "asset_id": "tok",
					"side": "BUY", "price": "0.5", "size": "1", "order_id": "0x1",
				}))
			}
			// trade 帧之后发 order 帧：若 trade 投递阻塞 reader，order 永远到不了
			writeMsg(t, c, map[string]any{
				"event_type": "order", "id": "0xafter",
				"status": "ORDER_STATUS_LIVE", "size_matched": "0", "timestamp": 1,
			})
			<-ctx.Done()
		},
	})
	defer srv.Close()

	f, err := NewFacade(wsURL,
		WithUserAuth(UserAuth{APIKey: "k", Passphrase: "p"}),
		withJitter(noJitter),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	orderCh, err := f.SubscribeOrders(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	tradeCh, err := f.SubscribeTrades(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	// 不消费 tradeCh，先等 order 事件 —— reader 未被塞满的 trade channel 拖住
	select {
	case got := <-orderCh:
		if got.OrderID != "0xafter" {
			t.Errorf("order id = %s", got.OrderID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("order event blocked behind full trade channel")
	}

	// order 事件晚于全部 trade 帧派发（reader 串行处理），此刻 tradeCh 内应恰好
	// 缓冲 64 条（1..64），65..70 被丢弃
	drained := 0
	firstID, lastID := "", ""
	for {
		select {
		case up := <-tradeCh:
			if drained == 0 {
				firstID = up.TradeID
			}
			lastID = up.TradeID
			drained++
			continue
		default:
		}
		break
	}
	if drained != 64 {
		t.Errorf("buffered trades = %d, want 64 (超量应丢弃而非阻塞)", drained)
	}
	if firstID != "1" || lastID != "64" {
		t.Errorf("buffered range = %s..%s, want 1..64", firstID, lastID)
	}
}

func TestSubscribeTrades_RequiresAuth(t *testing.T) {
	t.Parallel()

	f, err := NewFacade("ws://example.invalid", withJitter(noJitter))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.SubscribeTrades(context.Background(), "m")
	if !errors.Is(err, clob.ErrSign) {
		t.Errorf("err = %v, want ErrSign", err)
	}
}

func TestSubscribeTrades_EmptyURL(t *testing.T) {
	t.Parallel()
	f, _ := NewFacade("", WithUserAuth(UserAuth{APIKey: "k", Passphrase: "p"}))
	_, err := f.SubscribeTrades(context.Background(), "m")
	if !errors.Is(err, clob.ErrPrecondition) {
		t.Errorf("err = %v, want ErrPrecondition", err)
	}
}

// ctx 取消后 trade channel 关闭（与 SubscribeOrders 对齐）。
func TestSubscribeTrades_CtxCancelClosesChannel(t *testing.T) {
	t.Parallel()

	srv, wsURL := startMockWS(t, mockServerOpts{
		onConn: func(ctx context.Context, c *websocket.Conn, reqs <-chan json.RawMessage) {
			select {
			case <-reqs:
			case <-ctx.Done():
				return
			}
			<-ctx.Done()
		},
	})
	defer srv.Close()

	f, err := NewFacade(wsURL,
		WithUserAuth(UserAuth{APIKey: "k", Passphrase: "p"}),
		withJitter(noJitter),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := f.SubscribeTrades(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel should close after ctx cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel did not close in time")
	}
}
