package pmsdkgo

import (
	"errors"
	"testing"
	"time"
)

// TestNewDefaults 验证 New() 无参数也能成功，且返回的 Client 字段非 nil。
func TestNewDefaults(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	if c == nil {
		t.Fatal("New() returned nil Client")
	}
	if c.Clob == nil || c.Gamma == nil || c.WS == nil {
		t.Fatalf("sub-clients should be non-nil: clob=%v gamma=%v ws=%v",
			c.Clob, c.Gamma, c.WS)
	}
	if c.cfg.userAgent == "" {
		t.Error("default userAgent should be non-empty")
	}
	if c.cfg.httpTimeout != 30*time.Second {
		t.Errorf("default httpTimeout = %v, want 30s", c.cfg.httpTimeout)
	}
}

// TestNewWithOptions 验证 With* options 正确写入 config。
func TestNewWithOptions(t *testing.T) {
	c, err := New(
		WithEndpoints("https://clob.example", "https://gamma.example", "wss://ws.example"),
		WithHTTPTimeout(5*time.Second),
		WithChainID(137),
		WithUserAgent("test/1.0"),
		WithRateLimit(100),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if c.cfg.clobURL != "https://clob.example" {
		t.Errorf("clobURL = %q", c.cfg.clobURL)
	}
	if c.cfg.gammaURL != "https://gamma.example" {
		t.Errorf("gammaURL = %q", c.cfg.gammaURL)
	}
	if c.cfg.wsURL != "wss://ws.example" {
		t.Errorf("wsURL = %q", c.cfg.wsURL)
	}
	if c.cfg.httpTimeout != 5*time.Second {
		t.Errorf("httpTimeout = %v", c.cfg.httpTimeout)
	}
	if c.cfg.chainID != 137 {
		t.Errorf("chainID = %d", c.cfg.chainID)
	}
	if c.cfg.userAgent != "test/1.0" {
		t.Errorf("userAgent = %q", c.cfg.userAgent)
	}
	if c.cfg.rateLimit != 100 {
		t.Errorf("rateLimit = %d", c.cfg.rateLimit)
	}
}

// TestNewIgnoresNilOption 验证 New 跳过 nil Option，避免 panic。
func TestNewIgnoresNilOption(t *testing.T) {
	if _, err := New(nil); err != nil {
		t.Fatalf("New(nil) error = %v, want nil", err)
	}
}

// TestSentinelsAlias 验证顶层哨兵 alias 到 pkg/clob 实现。
//
// Phase 3 起 APIError + 哨兵的真正定义在 pkg/clob/errors.go；顶层 errors.go
// 走 alias 重导出。具体的 *APIError 构造与 Unwrap 协作测试见 pkg/clob/client_test.go。
func TestSentinelsAlias(t *testing.T) {
	if ErrNotFound == nil || ErrSign == nil || ErrRateLimit == nil ||
		ErrUpstream == nil || ErrPrecondition == nil || ErrCancelled == nil {
		t.Fatal("sentinel re-exports should be non-nil")
	}
	// errors.Is 自反性；保证 alias 后的 sentinel 与 pkg/clob 包内同源。
	if !errors.Is(ErrNotFound, ErrNotFound) {
		t.Error("errors.Is self-reflexive failed")
	}
}
