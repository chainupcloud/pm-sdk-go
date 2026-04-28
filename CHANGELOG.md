# Changelog

本仓所有显著变更记录于此。版本规范遵循 [Semantic Versioning](https://semver.org/lang/zh-CN/)。

## v0.1.0 — 2026-04-28

首个公开版本。按 [pm-sdk-go-contract.md](https://github.com/chainupcloud/pm-cup2026-liquidity/blob/main/docs/design-docs/pm-sdk-go-contract.md) 落地完整 v0.1.0 接口面，作为 `pm-cup2026-liquidity` M10 mirror 模块的下游 SDK 依赖（DEC-030 / DEC-035）。

风格对标 [`github.com/GoPolymarket/polymarket-go-sdk`](https://github.com/GoPolymarket/polymarket-go-sdk)：顶层 `Client` 门面 + `With*` Options + `pkg/<module>` 子包 + `examples/`。

### Phase 1 — 仓初始化与骨架

- 初始化 `go.mod`（`go 1.26`、module `github.com/chainupcloud/pm-sdk-go`），LICENSE 采用 Apache-2.0。
- 顶层 `Client` + `New()` + 全部 `With*` Options（契约 §3）。
- `pkg/{clob,gamma,ws,signer}/` 占位与 doc。
- 错误模型哨兵 + `APIError`（契约 §8）。
- CI workflow：`go vet` / `go test` / `go build` / `golangci-lint` / `codegen-drift`。

### Phase 2 — Codegen 流水线

- `scripts/codegen.sh`：通过 `gh api`（本地）/ `GH_TOKEN`（CI）拉取 `chainupcloud/pm-cup2026:pm-v2` 的 clob / gamma OpenAPI spec，跑 `oapi-codegen` 输出 `pkg/clob/generated.go` 与 `pkg/gamma/generated.go`。
- `codegen-config-{clob,gamma}.yaml`：`models + client + embedded-spec`，`response-type-suffix=Resp` 解决 `cancelOrders` 与 schema `CancelOrdersResponse` 命名重叠。
- `tools.go` 用 build tag `tools` 隔离 `oapi-codegen` build-time 依赖，避免被 `go mod tidy` 移除。
- CI `codegen-drift` job 注入 `PMCUP2026_READ_TOKEN` 后跑 codegen.sh，`git diff --exit-code` 校验 generated 不漂移；secret 缺失时打 notice 跳过，不阻塞 PR。

### Phase 3 — `pkg/clob` 业务门面

- `pkg/clob/client.go` 手写 `Facade`（`PlaceOrder` / `CancelOrder` / `GetOrder` / `ListOrders` / `GetBook` / `GetTrades`），内部组合 oapi-codegen 低层 `*Client` + `signer.Signer`，做请求构造、响应解码、wire ↔ SDK 枚举映射。
- `pkg/clob/types.go` 手写 SDK 类型（`OrderReq` / `Book` / `Level` / `OrderFilter` / `TradeFilter` / `OrderID` + `Sdk` 前缀的 `SdkOrder` / `SdkTrade` / `SdkSide` / `SdkOrderType` / `SdkOrderStatus`）。
- `pkg/clob/errors.go`：`*APIError` + 哨兵 `ErrSign` / `ErrRateLimit` / `ErrUpstream` / `ErrPrecondition` / `ErrNotFound` / `ErrCancelled` + `wrapHTTPError(resp, body)` + `wrapTransportError(ctx, err)`。
- 顶层 `errors.go` 哨兵与 `APIError` alias 重导出 `pkg/clob` 实现，避免 import cycle，同时保契约 §8 `pmsdkgo.ErrXxx` 可见性。
- `examples/place_order/`、`examples/cancel_order/` quickstart。

### Phase 4 — `pkg/gamma` 业务门面

- `GetEvent` / `ListEvents` / `GetMarket` / `GetToken` 与 `Event` / `Market` / `Token` / `EventFilter` 手写类型。
- `GetToken` 上游无独立端点，通过 `POST /markets/information` 反查并解析 `ClobTokenIDs`。
- 错误映射复用 `pkg/clob` 哨兵；`pkg/clob/errors.go` 新增 `SetSentinel(error)` setter 供 gamma 复用同一份 `*APIError`。
- ID 字段统一用 `string`（与上游 wire 对齐；契约 §5 `int64` 简化记录已在该 phase PR body 标注）。
- `examples/list_events/` quickstart。

### Phase 5 — `pkg/ws` 自动重连 + Sequence guard

- 手写 `*ws.Facade`（避让 codegen 命名）；与 clob / gamma 子包对称（顶层 `Client.WS *ws.Facade`）。
- `SubscribeBook(ctx, tokenIDs)` 订阅市场频道（`/ws/market`）：上游 `book` → `SNAPSHOT`，`price_change` → `DELTA`（按 `asset_id` 分组成 `BookUpdate`），SDK 自身在重连成功 + sequence 跳变时推 `RESET`。
- `SubscribeOrders(ctx, marketID)` 订阅用户频道（`/ws/user`）：必须 `WithUserAuth` 注入 `apiKey + passphrase`，缺失返 `ErrSign`；`ORDER_STATUS_*` 上游枚举 → SDK `SdkOrderStatus` 映射。
- 重连退避 1s/2s/4s/8s/16s/30s（封顶）+ 0–500ms jitter；nonce guard 用上游 `timestamp(ms)` 单调性 + frame hash 拦截倒退/重复帧并触发 `RESET`；心跳 10s 间隔 PING 文本帧（asyncapi spec），PONG 静默吞掉。
- WS lib 选用 `github.com/coder/websocket`（前身 `nhooyr.io/websocket`）。
- `examples/subscribe_book/` quickstart；asyncapi spec 快照 `docs/asyncapi-{market,user}.json`。

### Phase 6 — `pkg/signer` EIP-712 + pmcup26 5-field

- `pkg/signer/eip712.go` 通用 `NewEIP712Signer`：接受预算 `domainSeparator + 32-byte structHash` payload，输出 65-byte `(r||s||v)` 签名（`v ∈ {0,1}`）。
- `pkg/signer/clobauth.go` ClobAuth 5-field 域：`DomainSeparator` / `StructHash` / `BuildClobAuthDigest` 一站式入口；`ScopeIDFromHex` / `ToHex` 与后端 `[32]byte` bytes32 语义对齐；typeHash 与后端 `services/clob-service/internal/shared/crypto/eip712.go` 完全一致。
- `pkg/signer/order.go` CTFExchange Order 13-field 域：含 `scopeId bytes32` 多租户字段；`OrderStructHash` / `BuildOrderDigest` 与后端 `order_eip712.go` 字段顺序 + padding 完全一致。
- `pkg/signer/pmcup26.go` `NewPMCup26Signer` 内置 `chainID + scopeID + 可选 exchangeAddress`；提供 `SignClobAuth` / `SignOrder` 高级方法；`SchemaVersion="pmcup26-v1"`。
- `pkg/clob/client.go` `PlaceOrder` 检测 signer 是否实现 `orderSigner`（即 `*PMCup26Signer`），是则走 `SignOrder` 完整 13-field EIP-712 路径并在 wire 上填 `scopeId` / `signatureType` / `salt` / `nonce` / `feeRateBps`；否则降级 stub 签名。
- 黄金 fixture（`pkg/signer/testdata/golden.json`）覆盖 ClobAuth 3 组 + Order 3 组 + 域分隔符 4 组；`ecrecover` 反向验证 signer 地址；签名包覆盖率 94.8%。
- 新增依赖 `github.com/ethereum/go-ethereum v1.17.2`（与后端版本一致，避免 EIP-712 原语兼容风险）。

### Phase 7 — Observability + Contract Test

- `pkg/obs`：`Logger` / `Metrics` 接口 + `Nop` 实现；`pkg/obs/zapobs`（zap adapter）+ `pkg/obs/promobs`（prometheus adapter）作为 opt-in 子包，用户不导入则不链接。
- 顶层 `WithLogger` / `WithMetrics` Options + clob / gamma / ws / signer 各 facade 独立 plumbing。
- HTTP 请求边界（clob / gamma）counters / histograms / `request_id` 透传；`Warn` on transport / 4xx / 5xx，`Debug` on 2xx。
- WS connect / disconnect / reconnect / seq jump 带 channel label 指标；signer `Sign` / `SignOrder` 带 schema label 指标。
- 标准指标名常量 `pkg/obs.Metric*`。
- `internal/contracttest` framework：fixture loader + `httptest` mock server，build tag `contract`；placeholder fixtures 覆盖 `PlaceOrder` / `GetOrder` / `GetBook` / `ListEvents` / `GetMarket`。
- `docs/contract-test.md` 录回放工作流；CI 增加 optional `contract-tests` job（`continue-on-error: true`）。
- `examples/full_flow/` 端到端 quickstart（signer + obs + 5 个 SDK 调用）；`docs/api.md` 完整 API 参考（README 已链接）。
- 手写代码覆盖率 84.9%（不含 `generated.go` 与 `examples/`）。

### Phase 8 — Release（本版本）

- 补 `CHANGELOG.md`、polish `README.md` / `doc.go`，从 `chainupcloud/pm-cup2026-liquidity#157` 收口。
- 打 `v0.1.0` tag、发 GitHub release。

### 接口契约引用

权威契约源：[`pm-cup2026-liquidity:docs/design-docs/pm-sdk-go-contract.md`](https://github.com/chainupcloud/pm-cup2026-liquidity/blob/main/docs/design-docs/pm-sdk-go-contract.md)。本版本覆盖契约 §3（顶层门面与 Options）/ §4（clob）/ §5（gamma）/ §6（ws）/ §7（signer）/ §8（错误模型）。
