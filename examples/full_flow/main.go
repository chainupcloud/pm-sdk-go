// full_flow 是 Phase 7 综合 quickstart：演示 pm-sdk-go v0.1.0 的端到端使用形态。
//
// 串了 5 个步骤：
//
//  1. 构造 ClobAuth signer 走 5-field auth（pm-cup2026 鉴权）
//  2. 用 logger / metrics option 注入 zap + prometheus 适配器
//  3. PlaceOrder：通过 *clob.Facade 下一笔限价单
//  4. GetBook：拉一次订单簿快照
//  5. SubscribeBook：起 ws 频道；超时后退出
//  6. CancelOrder：撤掉刚才的订单
//
// 运行：
//
//	go run ./examples/full_flow
//
// 不需要 endpoint 可达；本 example 只在编译期被 CI 校验通过。各步骤遇 ErrCancelled
// / ErrUpstream 都按 expected 处理直接打印继续，确保 main 函数 graceful exit。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	pmsdkgo "github.com/chainupcloud/pm-sdk-go"
	"github.com/chainupcloud/pm-sdk-go/pkg/clob"
	"github.com/chainupcloud/pm-sdk-go/pkg/obs/promobs"
	"github.com/chainupcloud/pm-sdk-go/pkg/obs/zapobs"
	pmsigner "github.com/chainupcloud/pm-sdk-go/pkg/signer"
	"github.com/chainupcloud/pm-sdk-go/pkg/ws"
)

func main() {
	// 1. signer：用 ethcrypto 生成临时私钥构造 PMCup26Signer（无 exchange addr）。
	// 实际下单时 SignOrder 路径未注入 exchange 地址会返回 ErrNoExchangeAddress；
	// 本 example 演示注入形态，下单错误被吞回 expected 分支。
	priv, err := ethcrypto.GenerateKey()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen privkey:", err)
		os.Exit(1)
	}
	signer := pmsigner.NewPMCup26Signer(priv, [32]byte{}, 137)

	// 2. observability：zap dev logger + prometheus registry
	zl, _ := zap.NewDevelopment()
	defer func() { _ = zl.Sync() }()
	registry := prometheus.NewRegistry()

	cli, err := pmsdkgo.New(
		pmsdkgo.WithEndpoints(
			"https://clob.example.com",
			"https://gamma.example.com",
			"wss://ws.example.com",
		),
		pmsdkgo.WithHTTPTimeout(10*time.Second),
		pmsdkgo.WithChainID(137),
		pmsdkgo.WithSigner(signer),
		pmsdkgo.WithLogger(zapobs.New(zl)),
		pmsdkgo.WithMetrics(promobs.New(registry)),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "init sdk:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 3. PlaceOrder
	id, err := cli.Clob.PlaceOrder(ctx, clob.OrderReq{
		MarketID:    "0xmarketconditionid",
		TokenID:     "100200300",
		Side:        clob.SideBuy,
		OrderType:   clob.OrderTypeLimit,
		Price:       decimal.RequireFromString("0.55"),
		Size:        decimal.RequireFromString("10"),
		ClientOrder: "demo-full-001",
	})
	switch {
	case errors.Is(err, pmsdkgo.ErrUpstream), errors.Is(err, pmsdkgo.ErrCancelled), errors.Is(err, pmsdkgo.ErrSign):
		fmt.Println("step 3: PlaceOrder expected error against example endpoint:", err)
	case err != nil:
		fmt.Fprintln(os.Stderr, "step 3 PlaceOrder:", err)
	default:
		fmt.Printf("step 3: placed order id=%s\n", id)
	}

	// 4. GetBook
	if book, err := cli.Clob.GetBook(ctx, "100200300"); err != nil {
		fmt.Println("step 4: GetBook expected error:", err)
	} else {
		fmt.Printf("step 4: book bids=%d asks=%d\n", len(book.Bids), len(book.Asks))
	}

	// 5. SubscribeBook (ws)
	wsCtx, wsCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer wsCancel()
	ch, err := cli.WS.SubscribeBook(wsCtx, []string{"100200300"})
	if err != nil {
		fmt.Println("step 5: SubscribeBook init err:", err)
	} else {
		go func() {
			for evt := range ch {
				switch evt.Type {
				case ws.UpdateSnapshot:
					fmt.Printf("step 5: SNAPSHOT seq=%d\n", evt.Sequence)
				case ws.UpdateDelta:
					fmt.Printf("step 5: DELTA seq=%d\n", evt.Sequence)
				case ws.UpdateReset:
					fmt.Println("step 5: RESET")
				}
			}
		}()
		<-wsCtx.Done()
		fmt.Println("step 5: ws subscription window done")
	}

	// 6. CancelOrder（如果上一步 PlaceOrder 拿到 id 才有意义；否则用 placeholder）
	target := id
	if target == "" {
		target = clob.OrderID("0xnonexistent")
	}
	if err := cli.Clob.CancelOrder(ctx, target); err != nil {
		switch {
		case errors.Is(err, pmsdkgo.ErrNotFound):
			fmt.Println("step 6: order not found (expected for placeholder):", err)
		case errors.Is(err, pmsdkgo.ErrUpstream), errors.Is(err, pmsdkgo.ErrCancelled):
			fmt.Println("step 6: CancelOrder expected error:", err)
		default:
			fmt.Fprintln(os.Stderr, "step 6 CancelOrder:", err)
		}
	} else {
		fmt.Printf("step 6: cancelled %s\n", target)
	}

	// 收尾：打印 prometheus registry 已注册指标家族数（验证 obs 钩子真触发）
	mfs, _ := registry.Gather()
	fmt.Printf("done. prometheus metric families collected: %d\n", len(mfs))
}
