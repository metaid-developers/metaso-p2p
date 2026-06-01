package skillservice

import "testing"

func TestNormalisePaymentMetadata_MRC20DoesNotFallbackToProviderAddress(t *testing.T) {
	rec := &ServiceRecord{
		Price:           "1",
		Currency:        "MRC20",
		SettlementKind:  "mrc20",
		ProviderAddress: "provider-address",
		MRC20Ticker:     "FOO",
		MRC20Id:         "abc",
	}

	payment := normalisePaymentMetadata(rec)

	if payment.settlementKind != "mrc20" {
		t.Fatalf("settlementKind: got %q want mrc20", payment.settlementKind)
	}
	if payment.paymentChain != "btc" {
		t.Fatalf("paymentChain: got %q want btc", payment.paymentChain)
	}
	if payment.paymentAddress != "" {
		t.Fatalf("paymentAddress: got %q want empty", payment.paymentAddress)
	}
}
