// subscribe_book 是 Phase 5 quickstart：演示如何用 *pmsdkgo.Client.WS.SubscribeBook
// 订阅市场频道，并消费 SNAPSHOT / DELTA / RESET 三类事件。
//
// 运行：
//
//	go run ./examples/subscribe_book
//
// 不需要 endpoint 可达；本 example 只在编译期被 CI 校验通过。
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	pmsdkgo "github.com/chainupcloud/pm-sdk-go"
	"github.com/chainupcloud/pm-sdk-go/pkg/ws"
)

func main() {
	cli, err := pmsdkgo.New(
		pmsdkgo.WithEndpoints(
			"https://clob.example.com",
			"https://gamma.example.com",
			"wss://ws.example.com",
		),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "init sdk:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	tokenIDs := []string{"123456789"}
	ch, err := cli.WS.SubscribeBook(ctx, tokenIDs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "subscribe book:", err)
		os.Exit(1)
	}

	for evt := range ch {
		switch evt.Type {
		case ws.UpdateSnapshot:
			fmt.Printf("SNAPSHOT token=%s seq=%d bids=%d asks=%d\n",
				evt.TokenID, evt.Sequence, len(evt.Bids), len(evt.Asks))
		case ws.UpdateDelta:
			fmt.Printf("DELTA    token=%s seq=%d bids=%d asks=%d\n",
				evt.TokenID, evt.Sequence, len(evt.Bids), len(evt.Asks))
		case ws.UpdateReset:
			fmt.Println("RESET — clear local cache, expect SNAPSHOT next")
		}
	}
	fmt.Println("subscription closed")
}
