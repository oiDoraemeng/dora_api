package service

import (
	"errors"
	"net"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

// isSafeEasyPayFailoverError identifies failures that prove the primary did
// not create a payable upstream order. Timeouts, read/write failures, and
// malformed responses are intentionally excluded because their outcome is
// ambiguous and retrying another provider could lead to duplicate payment.
func isSafeEasyPayFailoverError(providerKey string, err error) bool {
	if err == nil || providerKey != payment.TypeEasyPay {
		return false
	}

	var configurationErr *payment.ProviderConfigurationError
	if errors.As(err, &configurationErr) {
		return true
	}
	var rejectedErr *payment.CreatePaymentRejectedError
	if errors.As(err, &rejectedErr) {
		return true
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && !dnsErr.Timeout() {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" && !opErr.Timeout() {
		return true
	}

	// TLS certificate and handshake errors happen before an HTTP request can
	// reach the EasyPay endpoint. The standard library exposes several concrete
	// TLS error types, so retain a narrow message check for their wrapped forms.
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "tls:") || strings.Contains(message, "x509:")
}
