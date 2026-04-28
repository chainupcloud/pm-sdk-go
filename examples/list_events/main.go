// list_events 是 Phase 4 quickstart：演示如何用 *pmsdkgo.Client.Gamma.ListEvents
// 翻页查询当前活跃的预测市场事件，并取出每个 event 下属 market 的 yes/no token id。
//
// 运行：
//
//	go run ./examples/list_events
//
// 不需要 endpoint 可达；本 example 只在编译期被 CI 校验通过。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	pmsdkgo "github.com/chainupcloud/pm-sdk-go"
	"github.com/chainupcloud/pm-sdk-go/pkg/gamma"
)

func main() {
	cli, err := pmsdkgo.New(
		pmsdkgo.WithEndpoints(
			"https://clob.example.com",
			"https://gamma.example.com",
			"wss://ws.example.com",
		),
		pmsdkgo.WithHTTPTimeout(10*time.Second),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "init sdk:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	active := true
	events, cursor, err := cli.Gamma.ListEvents(ctx, gamma.EventFilter{
		Limit:  20,
		Active: &active,
	})
	switch {
	case errors.Is(err, pmsdkgo.ErrUpstream), errors.Is(err, pmsdkgo.ErrCancelled):
		// 预期：example 不连真实端点，DNS / 连接失败被包装成 ErrUpstream / ErrCancelled。
		fmt.Println("expected error against example endpoint:", err)
		return
	case err != nil:
		fmt.Fprintln(os.Stderr, "list events:", err)
		os.Exit(1)
	}

	fmt.Printf("got %d events, next cursor=%q\n", len(events), cursor)
	for _, ev := range events {
		fmt.Printf("  event %s | %s | markets=%d\n", ev.ID, ev.Title, len(ev.Markets))
		for _, m := range ev.Markets {
			fmt.Printf("    market %s yes=%s no=%s\n", m.ID, m.YesTokenID, m.NoTokenID)
		}
	}
}
