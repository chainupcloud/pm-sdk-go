package clob

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type replaceOrdersWireRequest struct {
	CancelOrderIDs []string    `json:"cancelOrderIDs"`
	Orders         []SendOrder `json:"orders"`
}

type replaceOrdersWireResponse struct {
	StoppedAt  string                    `json:"stoppedAt,omitempty"`
	Cancels    []replaceCancelWireResult `json:"cancels"`
	Placements []replacePlaceWireResult  `json:"placements"`
	ErrorMsg   string                    `json:"errorMsg,omitempty"`
}

type replaceCancelWireResult struct {
	OrderID string `json:"orderID"`
	Status  string `json:"status"`
}

type replacePlaceWireResult struct {
	Index              int      `json:"index"`
	Success            bool     `json:"success"`
	ErrorMsg           string   `json:"errorMsg,omitempty"`
	OrderID            string   `json:"orderID"`
	TakingAmount       string   `json:"takingAmount"`
	MakingAmount       string   `json:"makingAmount"`
	Status             string   `json:"status"`
	TransactionsHashes []string `json:"transactionsHashes"`
	TradeIDs           []string `json:"tradeIDs"`
}

// ReplaceOrders 一次 HTTP 调用完成"撤旧单 + 下新单"。
//
// 服务端语义：先 cancel，再按 orders 顺序 place；business-level 单笔失败写入
// placements[i].errorMsg，不触发外层 error。403/503 fail-stop 会返回 envelope，本方法
// 仍尽量解析 cancels / placements，并把 HTTP 错误作为外层 err 返回，供调用方判断是否有
// 部分结果需要回滚或补偿。
func (f *Facade) ReplaceOrders(ctx context.Context, cancelIDs []OrderID, reqs []OrderReq) (ReplaceResult, error) {
	out := ReplaceResult{
		Cancels:    make([]CancelResult, len(cancelIDs)),
		Placements: make([]PlaceResult, len(reqs)),
	}
	for i, id := range cancelIDs {
		out.Cancels[i].ID = id
	}
	if len(cancelIDs) == 0 && len(reqs) == 0 {
		return out, nil
	}

	cancelWire := make([]string, 0, len(cancelIDs))
	cancelWireIdx := make([]int, 0, len(cancelIDs))
	for i, id := range cancelIDs {
		if id == "" {
			out.Cancels[i].Err = fmt.Errorf("%w: empty order id at index %d", ErrPrecondition, i)
			continue
		}
		cancelWire = append(cancelWire, string(id))
		cancelWireIdx = append(cancelWireIdx, i)
	}

	orderWire := make([]SendOrder, 0, len(reqs))
	orderWireIdx := make([]int, 0, len(reqs))
	signFailed := false
	for i := range reqs {
		body, err := f.signOne(ctx, reqs[i])
		if err != nil {
			out.Placements[i].Err = err
			signFailed = true
			continue
		}
		orderWire = append(orderWire, *body)
		orderWireIdx = append(orderWireIdx, i)
	}
	if signFailed && len(cancelWire) > 0 {
		return out, fmt.Errorf("%w: replace order signing failed; cancel phase not submitted", ErrSign)
	}
	if len(cancelWire) == 0 && len(orderWire) == 0 {
		return out, nil
	}

	bodyBytes, err := json.Marshal(replaceOrdersWireRequest{
		CancelOrderIDs: cancelWire,
		Orders:         orderWire,
	})
	if err != nil {
		return out, fmt.Errorf("%w: marshal ReplaceOrders request: %v", ErrUpstream, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(f.low.Server, "/")+"/orders/replace", bytes.NewReader(bodyBytes))
	if err != nil {
		return out, fmt.Errorf("%w: build ReplaceOrders request: %v", ErrUpstream, err)
	}
	req.Header.Set("Content-Type", "application/json")

	op := f.observe("ReplaceOrders", "POST", "/orders/replace")
	resp, err := f.low.Client.Do(req)
	op.done(resp, err)
	if err != nil {
		return out, wrapTransportError(ctx, err)
	}
	defer drainBody(resp)

	respBody, _ := io.ReadAll(resp.Body)
	if len(respBody) > 0 {
		var parsed replaceOrdersWireResponse
		if jerr := jsonUnmarshal(respBody, &parsed); jerr != nil {
			if resp.StatusCode >= 300 {
				return out, wrapHTTPError(resp, respBody)
			}
			return out, fmt.Errorf("%w: decode ReplaceOrdersResponse: %v", ErrUpstream, jerr)
		}
		out.StoppedAt = ReplaceStoppedAt(parsed.StoppedAt)
		out.ErrorMsg = parsed.ErrorMsg
		mapReplaceCancels(&out, cancelIDs, cancelWireIdx, parsed.Cancels)
		mapReplacePlacements(&out, orderWireIdx, parsed.Placements)
	}
	if resp.StatusCode >= 300 {
		return out, wrapHTTPError(resp, respBody)
	}
	return out, nil
}

func mapReplaceCancels(out *ReplaceResult, cancelIDs []OrderID, wireIdx []int, parsed []replaceCancelWireResult) {
	seen := make([]bool, len(out.Cancels))
	for i, item := range parsed {
		if i >= len(wireIdx) {
			break
		}
		idx := wireIdx[i]
		seen[idx] = true
		out.Cancels[idx].ID = cancelIDs[idx]
		switch item.Status {
		case "canceled", "not_found":
			out.Cancels[idx].Err = nil
		case "":
			out.Cancels[idx].Err = fmt.Errorf("%w: empty cancel status", ErrUpstream)
		default:
			out.Cancels[idx].Err = fmt.Errorf("%w: cancel %s", ErrUpstream, item.Status)
		}
	}
	for _, idx := range wireIdx {
		if !seen[idx] {
			out.Cancels[idx].Err = fmt.Errorf("%w: missing cancel response for order index %d", ErrUpstream, idx)
		}
	}
}

func mapReplacePlacements(out *ReplaceResult, wireIdx []int, parsed []replacePlaceWireResult) {
	seen := make([]bool, len(out.Placements))
	for _, item := range parsed {
		if item.Index < 0 || item.Index >= len(wireIdx) {
			continue
		}
		idx := wireIdx[item.Index]
		seen[idx] = true
		switch {
		case !item.Success:
			msg := item.ErrorMsg
			if msg == "" {
				msg = "place rejected"
			}
			out.Placements[idx].Err = fmt.Errorf("%w: %s", ErrUpstream, msg)
		case item.OrderID == "":
			msg := item.ErrorMsg
			if msg == "" {
				msg = "empty order id"
			}
			out.Placements[idx].Err = fmt.Errorf("%w: %s", ErrUpstream, msg)
		default:
			out.Placements[idx].OrderID = OrderID(item.OrderID)
		}
	}
	for _, idx := range wireIdx {
		if !seen[idx] {
			out.Placements[idx].Err = fmt.Errorf("%w: missing replace placement response for order index %d", ErrUpstream, idx)
		}
	}
}
