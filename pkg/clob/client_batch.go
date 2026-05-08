package clob

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// PlaceResult 是 PlaceOrders 中单笔订单的结果（与入参 reqs 同序）。
//
// Err 非 nil 时 OrderID 为零值；err 为 nil 时 OrderID 为 SDK 后端分配的 ID。
// 单笔失败不会让外层 PlaceOrders 返 err（外层 err 仅描述 transport 层故障）；
// 调用方应遍历 results，对 Err 非 nil 的条目走业务回滚。
type PlaceResult struct {
	OrderID OrderID
	Err     error
}

// CancelResult 是 CancelOrders 中单笔撤单的结果（与入参 ids 同序）。
//
// Err 非 nil 表示该 id 撤单失败（上游 not_canceled 列表中带 reason）；
// 单笔失败不会让外层 CancelOrders 返 err。
type CancelResult struct {
	ID  OrderID
	Err error
}

// PlaceOrders 批量下单（issue chainupcloud/pm-cup2026-liquidity#389）。
//
// 行为：
//  1. 每笔 OrderReq 独立用 signer 走 EIP-712 签名（与单笔 PlaceOrder 共用 signOne 逻辑）；
//     签名阶段任一失败即把对应 PlaceResult.Err 填上，不参与 wire payload，但会保留位置序。
//  2. 仅签名成功的 SendOrder 走一次 POST /orders 批量 RPC；上游响应 *[]SendOrderResponse
//     以 success 字段判定单笔成败，与原 SendOrder 数组按 wire 顺序对齐回填。
//  3. 外层 err 仅在 transport 故障 / 上游非 2xx / 解码失败时返非 nil。所有签名失败 +
//     单笔业务失败都通过 results[i].Err 暴露。
//
// 长度契约：返回 results 长度 == len(reqs)；index 与入参对齐；len(reqs)==0 → ([], nil)。
func (f *Facade) PlaceOrders(ctx context.Context, reqs []OrderReq) ([]PlaceResult, error) {
	results := make([]PlaceResult, len(reqs))
	if len(reqs) == 0 {
		return results, nil
	}

	// signedIdx[i] = 原始 reqs 索引；wireBodies / signed 均按 wire 发送顺序排列。
	wireBodies := make([]SendOrder, 0, len(reqs))
	signedIdx := make([]int, 0, len(reqs))
	for i := range reqs {
		body, err := f.signOne(ctx, reqs[i])
		if err != nil {
			results[i].Err = err
			continue
		}
		wireBodies = append(wireBodies, *body)
		signedIdx = append(signedIdx, i)
	}

	// 全员签名失败 → 不发 RPC，直接返结果（results 已填）。
	if len(wireBodies) == 0 {
		return results, nil
	}

	op := f.observe("PlaceOrders", "POST", "/orders")
	resp, err := f.low.PostOrders(ctx, wireBodies)
	op.done(resp, err)
	if err != nil {
		return results, wrapTransportError(ctx, err)
	}
	defer drainBody(resp)

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return results, wrapHTTPError(resp, respBody)
	}

	var parsed []SendOrderResponse
	if err := jsonUnmarshal(respBody, &parsed); err != nil {
		return results, fmt.Errorf("%w: decode []SendOrderResponse: %v", ErrUpstream, err)
	}

	// 上游应按 wire 顺序回 N 条结果；长度不一致视为 ErrUpstream 但保留可解析的部分。
	for k, item := range parsed {
		if k >= len(signedIdx) {
			break
		}
		origIdx := signedIdx[k]
		switch {
		case item.Success != nil && !*item.Success:
			msg := "place rejected"
			if item.ErrorMsg != nil && *item.ErrorMsg != "" {
				msg = *item.ErrorMsg
			}
			results[origIdx].Err = fmt.Errorf("%w: %s", ErrUpstream, msg)
		case item.OrderID == nil || *item.OrderID == "":
			msg := "empty order id"
			if item.ErrorMsg != nil && *item.ErrorMsg != "" {
				msg = *item.ErrorMsg
			}
			results[origIdx].Err = fmt.Errorf("%w: %s", ErrUpstream, msg)
		default:
			results[origIdx].OrderID = OrderID(*item.OrderID)
		}
	}
	if len(parsed) < len(signedIdx) {
		// 长度短于预期：未覆盖的 entries 标记为 upstream truncation。
		for _, idx := range signedIdx[len(parsed):] {
			results[idx].Err = fmt.Errorf("%w: missing response for order index %d", ErrUpstream, idx)
		}
	}
	return results, nil
}

// CancelOrders 批量撤单（issue chainupcloud/pm-cup2026-liquidity#389）。
//
// 行为：
//  1. 单次 DELETE /orders（body 为 JSON []string of order IDs，对齐上游 OpenAPI
//     CancelOrdersJSONBody0 联合分支）。
//  2. 上游响应 CancelOrdersResponse{canceled, not_canceled}：
//     canceled 列表中的 ID 对应 result.Err = nil；not_canceled map[id]reason 中的
//     ID 对应 result.Err = wrapped ErrUpstream。
//  3. 外层 err 仅在 transport / 非 2xx / 解码失败时返非 nil。空 ids 短路返 ([], nil)；
//     ids 中含空字符串视为单笔 ErrPrecondition。
func (f *Facade) CancelOrders(ctx context.Context, ids []OrderID) ([]CancelResult, error) {
	results := make([]CancelResult, len(ids))
	if len(ids) == 0 {
		return results, nil
	}

	// 过滤空 ID：保留位置序，标 ErrPrecondition；只把非空的发到上游。
	wire := make([]string, 0, len(ids))
	wireIdx := make([]int, 0, len(ids))
	for i, id := range ids {
		results[i].ID = id
		if id == "" {
			results[i].Err = fmt.Errorf("%w: empty order id at index %d", ErrPrecondition, i)
			continue
		}
		wire = append(wire, string(id))
		wireIdx = append(wireIdx, i)
	}
	if len(wire) == 0 {
		return results, nil
	}

	// CancelOrdersJSONBody 是 oapi-codegen 生成的 union 占位（只有未导出 union 字段），
	// 默认 json.Marshal 输出 {} 不匹配上游 schema。直接用 CancelOrdersWithBody 把
	// JSON []string 写进 body，对齐 CancelOrdersJSONBody0 = []string 分支。
	bodyBytes, err := json.Marshal(wire)
	if err != nil {
		// 极端不可达；保留为外层 err 而非 per-id err。
		return results, fmt.Errorf("%w: marshal cancel ids: %v", ErrUpstream, err)
	}

	op := f.observe("CancelOrders", "DELETE", "/orders")
	resp, err := f.low.CancelOrdersWithBody(ctx, "application/json", bytes.NewReader(bodyBytes))
	op.done(resp, err)
	if err != nil {
		return results, wrapTransportError(ctx, err)
	}
	defer drainBody(resp)

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return results, wrapHTTPError(resp, respBody)
	}

	var parsed CancelOrdersResponse
	if err := jsonUnmarshal(respBody, &parsed); err != nil {
		return results, fmt.Errorf("%w: decode CancelOrdersResponse: %v", ErrUpstream, err)
	}

	// 默认假设全部成功；再用 not_canceled map 覆盖具体失败项。
	notCanceled := map[string]string{}
	if parsed.NotCanceled != nil {
		notCanceled = *parsed.NotCanceled
	}
	canceledSet := map[string]struct{}{}
	if parsed.Canceled != nil {
		for _, id := range *parsed.Canceled {
			canceledSet[id] = struct{}{}
		}
	}
	for _, idx := range wireIdx {
		idStr := string(ids[idx])
		if reason, bad := notCanceled[idStr]; bad {
			msg := reason
			if msg == "" {
				msg = "cancel rejected"
			}
			results[idx].Err = fmt.Errorf("%w: %s", ErrUpstream, msg)
			continue
		}
		// 上游若严格列出 canceled，未出现 → 视为静默失败（保守哨兵）。
		// 老路径仅用 canceled 也判定 ok：若两者都为 nil 则认为成功（与单笔 CancelOrder 同步）。
		if parsed.Canceled != nil && len(*parsed.Canceled) > 0 {
			if _, ok := canceledSet[idStr]; !ok {
				// canceled 数组明确返了但不含本 ID → 视为失败但无 reason。
				if _, listed := notCanceled[idStr]; !listed {
					results[idx].Err = fmt.Errorf("%w: not in canceled list", ErrUpstream)
					continue
				}
			}
		}
		// 默认成功（Err 留 nil）。
	}
	return results, nil
}

