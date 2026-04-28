# pm-sdk-go

`github.com/chainupcloud/pm-sdk-go` 是 [pm-cup2026](https://github.com/chainupcloud/pm-cup2026) 预测市场的 Go 客户端 SDK。

风格对标 [`github.com/GoPolymarket/polymarket-go-sdk`](https://github.com/GoPolymarket/polymarket-go-sdk)（顶层 `Client` 门面 + `With*` Options + `pkg/<module>` 子包 + `examples/`）。

## 状态

> v0.1.0 开发中 — Phase 2 已落地 codegen pipeline，`pkg/clob` / `pkg/gamma` 暴露 oapi-codegen 生成的低层 *Client；高层门面与业务方法在 Phase 3+ 落地。

接口契约权威源：<https://github.com/chainupcloud/pm-cup2026-liquidity/blob/main/docs/design-docs/pm-sdk-go-contract.md>。

## 快速开始（Phase 2）

```go
package main

import (
    "log"
    "time"

    pmsdk "github.com/chainupcloud/pm-sdk-go"
)

func main() {
    c, err := pmsdk.New(
        pmsdk.WithEndpoints(
            "https://clob.example.com",
            "https://gamma.example.com",
            "wss://ws.example.com",
        ),
        pmsdk.WithHTTPTimeout(10*time.Second),
        pmsdk.WithChainID(137),
        pmsdk.WithUserAgent("my-app/1.0"),
    )
    if err != nil {
        log.Fatal(err)
    }
    _ = c.Clob // Phase 3 起可用
    _ = c.Gamma
    _ = c.WS
}
```

实际业务调用（`PlaceOrder` / `SubscribeBook` 等）将随 Phase 3+ 落地。

## OpenAPI 来源

`scripts/codegen.sh` 从 `chainupcloud/pm-cup2026:pm-v2` 拉取以下 spec 生成 `pkg/clob/generated.go` / `pkg/gamma/generated.go`：

| 子包 | OpenAPI 路径 |
|------|--------------|
| `pkg/clob` | `services/clob-service/docs/openapi.yaml` |
| `pkg/gamma` | `services/gamma-service/api/openapi.yaml` |

## Codegen

```bash
# 本地（需 gh CLI 已登录可访问 chainupcloud/pm-cup2026 的账号）
./scripts/codegen.sh

# 也可用环境变量 token，无需 gh CLI
GH_TOKEN=ghp_xxx ./scripts/codegen.sh
```

不要手改 `pkg/{clob,gamma}/generated.go`；要改：(1) 改上游 spec；或 (2) 改 `codegen-config-{clob,gamma}.yaml`，再重跑脚本。CI 里 `codegen-drift` job 会拉同一份 spec 重跑，与仓内 generated 文件做 `git diff --exit-code` 校验漂移。

## 路线图

完整 8 phase 计划见 [`pm-cup2026-liquidity` 仓 sdk-agent-brief](https://github.com/chainupcloud/pm-cup2026-liquidity/blob/main/docs/sdk-agent-brief.md)。

| Phase | 目标 |
|-------|------|
| 1 | 仓初始化 + 顶层 `Client` / `Option` / 子包占位 + CI |
| 2 | `scripts/codegen.sh` 实拉 OpenAPI + `oapi-codegen` 输出 generated.go（**当前**） |
| 3 | `pkg/clob` 业务方法 + types + 错误模型 + examples |
| 4 | `pkg/gamma` 业务方法 + types + examples |
| 5 | `pkg/ws` 自动重连 / Sequence guard / `SubscribeBook` |
| 6 | `pkg/signer` EIP-712 + pmcup26 5-field auth |
| 7 | observability（zap / prometheus）+ contract test |
| 8 | `v0.1.0` tag + GitHub release |

## 开发

```bash
go vet ./...
go test ./...
golangci-lint run
```

要求 Go 1.26 及以上（与 `chainupcloud/pm-cup2026` 主仓对齐）。

## License

[Apache License 2.0](LICENSE).
