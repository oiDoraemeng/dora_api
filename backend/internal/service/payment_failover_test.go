package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestDefaultLoadBalancerSelectCandidatesUsesSortOrderAndLimits(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)

	primary, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeEasyPay).
		SetName("primary").
		SetConfig(`{"pid":"primary"}`).
		SetSupportedTypes(payment.TypeAlipay).
		SetLimits(`{"alipay":{"singleMax":50}}`).
		SetEnabled(true).
		SetSortOrder(10).
		Save(ctx)
	require.NoError(t, err)
	backup, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeEasyPay).
		SetName("backup").
		SetConfig(`{"pid":"backup"}`).
		SetSupportedTypes(payment.TypeAlipay).
		SetEnabled(true).
		SetSortOrder(20).
		Save(ctx)
	require.NoError(t, err)

	lb := payment.NewDefaultLoadBalancer(client, nil)
	selections, err := lb.SelectCandidates(ctx, payment.TypeEasyPay, payment.TypeAlipay, 40)
	require.NoError(t, err)
	require.Equal(t, []string{strconv.FormatInt(primary.ID, 10), strconv.FormatInt(backup.ID, 10)}, []string{selections[0].InstanceID, selections[1].InstanceID})

	selections, err = lb.SelectCandidates(ctx, payment.TypeEasyPay, payment.TypeAlipay, 60)
	require.NoError(t, err)
	require.Len(t, selections, 1)
	require.Equal(t, strconv.FormatInt(backup.ID, 10), selections[0].InstanceID)
}

func TestSelectCreateOrderInstancesUsesEasyPayVisibleSourceForFailover(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)

	for _, instance := range []struct {
		providerKey string
		name        string
		config      string
		sortOrder   int
	}{
		{payment.TypeAlipay, "official", `{"appId":"official"}`, 0},
		{payment.TypeEasyPay, "primary", `{"pid":"primary"}`, 10},
		{payment.TypeEasyPay, "backup", `{"pid":"backup"}`, 20},
	} {
		_, err := client.PaymentProviderInstance.Create().
			SetProviderKey(instance.providerKey).
			SetName(instance.name).
			SetConfig(instance.config).
			SetSupportedTypes(payment.TypeAlipay).
			SetEnabled(true).
			SetSortOrder(instance.sortOrder).
			Save(ctx)
		require.NoError(t, err)
	}

	configService := &PaymentConfigService{
		entClient: client,
		settingRepo: &paymentConfigSettingRepoStub{values: map[string]string{
			SettingPaymentVisibleMethodAlipaySource: VisibleMethodSourceEasyPayAlipay,
		}},
	}
	inner := payment.NewDefaultLoadBalancer(client, nil)
	svc := &PaymentService{
		configService: configService,
		loadBalancer:  newVisibleMethodLoadBalancer(inner, configService),
	}

	selections, err := svc.selectCreateOrderInstances(ctx, CreateOrderRequest{PaymentType: payment.TypeAlipay}, &PaymentConfig{
		LoadBalanceStrategy: string(payment.StrategyPriorityFailover),
	}, 10)
	require.NoError(t, err)
	require.Len(t, selections, 2)
	require.Equal(t, payment.TypeEasyPay, selections[0].ProviderKey)
	require.Equal(t, "primary", selections[0].Config["pid"])
	require.Equal(t, "backup", selections[1].Config["pid"])
}

func TestEasyPayFailoverErrorClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "local configuration error is safe",
			err:  &payment.ProviderConfigurationError{Message: "missing pid"},
			want: true,
		},
		{
			name: "explicit upstream rejection is safe",
			err:  fmt.Errorf("create: %w", &payment.CreatePaymentRejectedError{Message: "channel unavailable"}),
			want: true,
		},
		{
			name: "dns failure is safe",
			err:  &net.DNSError{Name: "pay.example.test", Err: "no such host"},
			want: true,
		},
		{
			name: "connection refused before request is safe",
			err:  &net.OpError{Op: "dial", Err: errors.New("connection refused")},
			want: true,
		},
		{
			name: "timeout is ambiguous",
			err:  context.DeadlineExceeded,
			want: false,
		},
		{
			name: "read failure is ambiguous",
			err:  &net.OpError{Op: "read", Err: errors.New("connection reset")},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isSafeEasyPayFailoverError(payment.TypeEasyPay, tt.err))
		})
	}
	require.False(t, isSafeEasyPayFailoverError(payment.TypeAlipay, &payment.CreatePaymentRejectedError{}))
}

func TestValidateEasyPayFailoverSelectionsAllowsPopup(t *testing.T) {
	t.Parallel()

	err := validateEasyPayFailoverSelections([]*payment.InstanceSelection{
		{ProviderKey: payment.TypeEasyPay, PaymentMode: "qrcode"},
		{ProviderKey: payment.TypeEasyPay, PaymentMode: "popup"},
	})
	require.NoError(t, err)

	err = validateEasyPayFailoverSelections([]*payment.InstanceSelection{
		{ProviderKey: payment.TypeEasyPay, PaymentMode: "popup"},
		{ProviderKey: payment.TypeEasyPay, PaymentMode: "popup"},
	})
	require.NoError(t, err)
}

func TestInvokeProviderCandidatesFallsBackToEasyPayBackupAndUpdatesSnapshot(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)

	var receivedPIDs []string
	var receivedOrderIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/mapi.php", r.URL.Path)
		require.NoError(t, r.ParseForm())
		pid := r.Form.Get("pid")
		receivedPIDs = append(receivedPIDs, pid)
		receivedOrderIDs = append(receivedOrderIDs, r.Form.Get("out_trade_no"))
		w.Header().Set("Content-Type", "application/json")
		if pid == "primary" {
			_, _ = w.Write([]byte(`{"code":0,"msg":"channel unavailable"}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":1,"trade_no":"backup-trade","qrcode":"backup-qr"}`))
	}))
	defer server.Close()

	user, err := client.User.Create().
		SetEmail("failover@example.com").
		SetPasswordHash("hash").
		SetUsername("failover-user").
		Save(ctx)
	require.NoError(t, err)

	primary := easyPayFailoverSelection("1", "primary", server.URL)
	backup := easyPayFailoverSelection("2", "backup", server.URL)
	svc := &PaymentService{entClient: client}
	req := CreateOrderRequest{
		UserID:      user.ID,
		PaymentType: payment.TypeAlipay,
		OrderType:   payment.OrderTypeBalance,
		ClientIP:    "127.0.0.1",
		SrcHost:     "app.example.test",
	}
	order, err := svc.createOrderInTx(
		ctx,
		req,
		&User{ID: user.ID, Email: user.Email, Username: user.Username},
		nil,
		&PaymentConfig{MaxPendingOrders: 3, OrderTimeoutMin: 30},
		10,
		10,
		0,
		10,
		primary,
	)
	require.NoError(t, err)

	resp, err := svc.invokeProviderCandidates(ctx, order, req, &PaymentConfig{}, 10, "10.00", 10, nil, []*payment.InstanceSelection{primary, backup})
	require.NoError(t, err)
	require.Equal(t, "backup-qr", resp.QRCode)
	require.Equal(t, []string{"primary", "backup"}, receivedPIDs)
	require.Len(t, receivedOrderIDs, 2)
	require.Equal(t, order.OutTradeNo, receivedOrderIDs[0])
	require.Equal(t, order.OutTradeNo, receivedOrderIDs[1])

	updated, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, "2", failoverValueOrEmpty(updated.ProviderInstanceID))
	require.Equal(t, payment.TypeEasyPay, failoverValueOrEmpty(updated.ProviderKey))
	require.Equal(t, "backup", updated.ProviderSnapshot["merchant_id"])
	require.Equal(t, "backup-qr", failoverValueOrEmpty(updated.QrCode))

	failedAttempts, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.ActionEQ("ORDER_PROVIDER_FAILED")).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, failedAttempts)
}

func easyPayFailoverSelection(id, pid, apiBase string) *payment.InstanceSelection {
	return &payment.InstanceSelection{
		InstanceID:     id,
		ProviderKey:    payment.TypeEasyPay,
		SupportedTypes: payment.TypeAlipay,
		PaymentMode:    "qrcode",
		Config: map[string]string{
			"pid":       pid,
			"pkey":      "test-key",
			"apiBase":   apiBase,
			"notifyUrl": "https://app.example.test/api/v1/payment/webhook/easypay",
			"returnUrl": "https://app.example.test/payment/result",
		},
	}
}

func failoverValueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func TestEasyPayFailoverDoesNotRetryAmbiguousFailure(t *testing.T) {
	t.Parallel()

	require.False(t, isSafeEasyPayFailoverError(payment.TypeEasyPay, fmt.Errorf("request timed out: %w", context.DeadlineExceeded)))
	require.False(t, isSafeEasyPayFailoverError(payment.TypeEasyPay, &net.OpError{Op: "write", Err: context.DeadlineExceeded}))
}
