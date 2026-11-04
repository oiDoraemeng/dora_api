package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

// queryYPayOrder supports the order endpoint used by YPay-based EasyPay
// gateways. Some deployments reverse the documented lookup types: 2 looks up
// out_trade_no, while 1 looks up trade_no. Try the other type only for an
// explicit order-not-found response, and always bind the returned order to
// both the requested out_trade_no and this instance's merchant ID.
func (e *EasyPay) queryYPayOrder(ctx context.Context, outTradeNo string) (*easyPayQueryResult, error) {
	for _, lookupType := range []string{"2", "1"} {
		body, httpStatus, err := e.postRaw(ctx, e.apiBase()+"/api/findorder", map[string]string{
			"order_no": outTradeNo,
			"type":     lookupType,
		})
		if err != nil {
			return nil, fmt.Errorf("easypay YPay query: %w", err)
		}
		if httpStatus < http.StatusOK || httpStatus >= http.StatusMultipleChoices {
			return nil, fmt.Errorf("easypay YPay query HTTP %d", httpStatus)
		}
		var resp struct {
			Code json.RawMessage `json:"code"`
			Msg  string          `json:"msg"`
			Data *struct {
				MerchantID json.RawMessage `json:"id"`
				OutTradeNo string          `json:"out_trade_no"`
				TradeNo    string          `json:"trade_no"`
				Money      json.RawMessage `json:"money"`
				Status     json.RawMessage `json:"status"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("easypay parse YPay query: %w", err)
		}
		code := easyPayJSONScalar(resp.Code)
		if code != "200" {
			if lookupType == "2" && code == "201" && easyPayYPayOrderNotFound(resp.Msg) {
				continue
			}
			return nil, fmt.Errorf("easypay YPay query failed (code %s): %s", code, strings.TrimSpace(resp.Msg))
		}
		if resp.Data == nil {
			return nil, fmt.Errorf("easypay YPay query missing order data")
		}
		data := resp.Data
		if strings.TrimSpace(data.OutTradeNo) != strings.TrimSpace(outTradeNo) {
			return nil, fmt.Errorf("easypay YPay query out_trade_no mismatch")
		}
		// This is a public lookup API: never assume its order belongs to the
		// configured merchant merely because it was found by an order number.
		if easyPayJSONScalar(data.MerchantID) != strings.TrimSpace(e.config["pid"]) {
			return nil, fmt.Errorf("easypay YPay query merchant id mismatch")
		}
		money := easyPayJSONScalar(data.Money)
		amount, err := strconv.ParseFloat(money, 64)
		if err != nil || math.IsNaN(amount) || math.IsInf(amount, 0) || amount <= 0 {
			return nil, fmt.Errorf("easypay YPay query invalid amount")
		}
		tradeNo := strings.TrimSpace(data.TradeNo)
		if tradeNo == "" {
			return nil, fmt.Errorf("easypay YPay query missing trade_no")
		}
		status := payment.ProviderStatusPending
		if easyPayJSONScalar(data.Status) == strconv.Itoa(easypayStatusPaid) {
			status = payment.ProviderStatusPaid
		}
		return &easyPayQueryResult{status: status, money: money, tradeNo: tradeNo}, nil
	}
	return nil, fmt.Errorf("easypay YPay query order not found")
}

func easyPayYPayOrderNotFound(msg string) bool {
	return strings.Contains(msg, "此订单号不是有效订单号") ||
		strings.Contains(msg, "订单号不存在") || strings.Contains(msg, "订单不存在") ||
		strings.Contains(strings.ToLower(msg), "order not found")
}
