package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

// This is the response shape returned by ezfpy.cn for an existing order.
// data.id is the merchant ID, and type=2 searches the merchant order number.
const ypayPaidOrder = `{"code":200,"msg":"获取成功!","data":{"id":1001,"type":"alipay","trade_no":"2026101600001","out_trade_no":"sub2_ORDER1","name":"recharge","money":"10.00","status":1}}`

func newYPayTestProvider(t *testing.T, handler http.HandlerFunc) *EasyPay {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	e := newTestEasyPay(t, server.URL+"/gateway")
	e.config["pid"] = "1001"
	return e
}

func TestEasyPayYPayQueryFallback(t *testing.T) {
	t.Parallel()
	for _, legacyStatus := range []int{http.StatusNotFound, http.StatusMethodNotAllowed} {
		for _, merchantLookupType := range []string{"2", "1"} {
			t.Run(fmt.Sprintf("HTTP%d/type%s", legacyStatus, merchantLookupType), func(t *testing.T) {
				t.Parallel()
				var legacyCalls, ypayCalls atomic.Int32
				e := newYPayTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost {
						t.Errorf("method = %s, want POST", r.Method)
					}
					switch r.URL.Path {
					case "/gateway/api.php":
						legacyCalls.Add(1)
						w.WriteHeader(legacyStatus)
						_, _ = w.Write([]byte("<html>endpoint unavailable</html>"))
					case "/gateway/api/findorder":
						call := ypayCalls.Add(1)
						if err := r.ParseForm(); err != nil {
							t.Errorf("ParseForm: %v", err)
						}
						if len(r.URL.Query()) != 0 || len(r.PostForm) != 2 || r.PostForm.Get("order_no") != "sub2_ORDER1" {
							t.Errorf("unexpected public query parameters: query=%v form=%v", r.URL.Query(), r.PostForm)
						}
						// Credentials belong only to the legacy authenticated API.
						if r.PostForm.Get("key") != "" || r.PostForm.Get("pkey") != "" {
							t.Error("merchant secret was sent to the public lookup API")
						}
						if merchantLookupType == "1" && call%2 == 1 {
							if r.PostForm.Get("type") != "2" {
								t.Errorf("first lookup type = %q, want 2", r.PostForm.Get("type"))
							}
							_, _ = w.Write([]byte(`{"code":201,"msg":"此订单号不是有效订单号"}`))
							return
						}
						if r.PostForm.Get("type") != merchantLookupType {
							t.Errorf("lookup type = %q, want %s", r.PostForm.Get("type"), merchantLookupType)
						}
						_, _ = w.Write([]byte(ypayPaidOrder))
					default:
						t.Errorf("unexpected request: %s", r.URL.Path)
						w.WriteHeader(http.StatusInternalServerError)
					}
				})
				response, err := e.QueryOrder(context.Background(), "sub2_ORDER1")
				if err != nil {
					t.Fatalf("QueryOrder: %v", err)
				}
				if response.Status != payment.ProviderStatusPaid || response.Amount != 10 || response.TradeNo != "2026101600001" || response.Metadata["pid"] != "1001" {
					t.Fatalf("unexpected query result: %+v", response)
				}
				// Exercise the same signature verification and confirmation used
				// by the webhook, without fulfilling or crediting any real order.
				params := map[string]string{
					"pid": "1001", "out_trade_no": "sub2_ORDER1", "trade_no": "2026101600001",
					"type": "alipay", "money": "10.00", "trade_status": tradeStatusSuccess,
				}
				params["sign"] = easyPaySign(params, e.config["pkey"])
				form := url.Values{}
				for key, value := range params {
					form.Set(key, value)
				}
				notification, err := e.VerifyNotification(context.Background(), form.Encode(), nil)
				if err != nil {
					t.Fatalf("VerifyNotification: %v", err)
				}
				if err := e.ConfirmNotification(context.Background(), notification); err != nil {
					t.Fatalf("ConfirmNotification: %v", err)
				}
				wantYPayCalls := int32(2)
				if merchantLookupType == "1" {
					wantYPayCalls = 4
				}
				if legacyCalls.Load() != 2 || ypayCalls.Load() != wantYPayCalls {
					t.Fatalf("query calls: legacy=%d YPay=%d, want 2/%d", legacyCalls.Load(), ypayCalls.Load(), wantYPayCalls)
				}
			})
		}
	}
}

func TestEasyPayYPayRejectsInvalidResponses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, body, wantErr string
		status              int
	}{
		{"foreign merchant", strings.Replace(ypayPaidOrder, `"id":1001`, `"id":2000`, 1), "merchant id mismatch", 200},
		{"missing merchant", strings.Replace(ypayPaidOrder, `"id":1001,`, "", 1), "merchant id mismatch", 200},
		{"foreign order", strings.Replace(ypayPaidOrder, "sub2_ORDER1", "sub2_OTHER", 1), "out_trade_no mismatch", 200},
		{"missing order", strings.Replace(ypayPaidOrder, "sub2_ORDER1", "", 1), "out_trade_no mismatch", 200},
		{"missing trade number", strings.Replace(ypayPaidOrder, "2026101600001", "", 1), "missing trade_no", 200},
		{"missing amount", strings.Replace(ypayPaidOrder, `"money":"10.00",`, "", 1), "invalid amount", 200},
		{"nonfinite amount", strings.Replace(ypayPaidOrder, "10.00", "NaN", 1), "invalid amount", 200},
		{"infinite amount", strings.Replace(ypayPaidOrder, "10.00", "+Inf", 1), "invalid amount", 200},
		{"negative amount", strings.Replace(ypayPaidOrder, "10.00", "-10.00", 1), "invalid amount", 200},
		{"wrong success code", strings.Replace(ypayPaidOrder, `"code":200`, `"code":1`, 1), "query failed", 200},
		{"failure wins over paid", strings.Replace(ypayPaidOrder, `"code":200`, `"code":201`, 1), "query failed", 200},
		{"fractional code", strings.Replace(ypayPaidOrder, `"code":200`, `"code":200.1`, 1), "query failed", 200},
		{"missing data", `{"code":200}`, "missing order data", 200},
		{"null data", `{"code":200,"data":null}`, "missing order data", 200},
		{"HTML response", "<html>error</html>", "parse YPay query", 200},
		{"HTTP error with paid body", ypayPaidOrder, "query HTTP 503", 503},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var ypayCalls atomic.Int32
			e := newYPayTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/gateway/api.php" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				ypayCalls.Add(1)
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			})
			_, err := e.QueryOrder(context.Background(), "sub2_ORDER1")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("QueryOrder error = %v, want %q", err, tt.wantErr)
			}
			if ypayCalls.Load() != 1 {
				t.Fatalf("YPay calls = %d, want 1", ypayCalls.Load())
			}
		})
	}
}

func TestEasyPayQueryDoesNotFallbackOnLegacyAnswer(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, body string
		status     int
		wantErr    bool
	}{
		{"unpaid", `{"code":1,"status":0}`, 200, false},
		{"rejected query", `{"code":0,"msg":"order not found"}`, 200, true},
		{"HTML with HTTP 200", "<html>error</html>", 200, true},
		{"unauthorized", ypayPaidOrder, 401, true},
		{"server error", `{"code":1,"status":1,"money":"10.00"}`, 503, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newYPayTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/gateway/api.php" {
					t.Errorf("unexpected fallback request: %s", r.URL.Path)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			})
			response, err := e.QueryOrder(context.Background(), "sub2_ORDER1")
			if (err != nil) != tt.wantErr {
				t.Fatalf("QueryOrder error = %v, wantErr=%v", err, tt.wantErr)
			}
			if err == nil && response.Status != payment.ProviderStatusPending {
				t.Fatalf("status = %q, want pending", response.Status)
			}
		})
	}
}

func TestEasyPayYPayConfirmNotificationChecksPayment(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, body, wantErr string
	}{
		{"unpaid", strings.Replace(ypayPaidOrder, `"status":1`, `"status":0`, 1), "not paid"},
		{"amount mismatch", strings.Replace(ypayPaidOrder, "10.00", "0.01", 1), "amount mismatch"},
		{"trade number mismatch", strings.Replace(ypayPaidOrder, "2026101600001", "2026101699999", 1), "trade_no mismatch"},
		{"PHP string scalars", strings.ReplaceAll(strings.Replace(ypayPaidOrder, `"id":1001`, `"id":"1001"`, 1), `"status":1`, `"status":"1"`), ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newYPayTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/gateway/api.php" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_, _ = w.Write([]byte(tt.body))
			})
			err := e.ConfirmNotification(context.Background(), confirmTestNotification())
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ConfirmNotification: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ConfirmNotification error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
