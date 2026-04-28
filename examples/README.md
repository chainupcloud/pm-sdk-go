# examples

每个示例放在独立子目录，便于 `go run ./examples/<name>` 独立编译运行；
endpoint 仅作演示填充，不要求真实可达，CI 只在编译期校验。

| 子目录 | Phase | 说明 |
|--------|-------|------|
| `place_order/` | 3 | 下单（无 signer 时返回 ErrSign，演示哨兵协作） |
| `cancel_order/` | 3 | 撤单（按 OrderID） |
| `list_events/` | 4 | 查 event/market 元数据（待 Phase 4 落地） |
| `subscribe_book/` | 5 | 订阅深度行情（待 Phase 5 落地） |
