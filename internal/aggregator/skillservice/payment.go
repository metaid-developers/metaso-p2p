package skillservice

import (
	"strconv"
	"strings"
)

type paymentMetadata struct {
	currency       string
	settlementKind string
	paymentChain   string
	paymentAddress string
	mrc20Ticker    any
	mrc20Id        any
}

func normalisePaymentMetadata(rec *ServiceRecord) paymentMetadata {
	if rec == nil {
		return paymentMetadata{}
	}

	currency := normaliseDisplayCurrency(rec.Currency)
	settlementKind := normaliseSettlementKind(rec.SettlementKind, currency, rec.MRC20Ticker, rec.MRC20Id)
	paymentChain := strings.ToLower(strings.TrimSpace(rec.PaymentChain))
	if paymentChain == "" {
		paymentChain = defaultPaymentChain(settlementKind, currency)
	}

	paymentAddress := strings.TrimSpace(rec.PaymentAddress)
	if paymentAddress == "" && settlementKind == "native" && isPositivePrice(rec.Price) {
		paymentAddress = strings.TrimSpace(rec.ProviderAddress)
	}

	var ticker any
	var mrc20Id any
	if rec.MRC20Ticker != "" {
		ticker = rec.MRC20Ticker
	}
	if rec.MRC20Id != "" {
		mrc20Id = rec.MRC20Id
	}

	return paymentMetadata{
		currency:       currency,
		settlementKind: settlementKind,
		paymentChain:   paymentChain,
		paymentAddress: paymentAddress,
		mrc20Ticker:    ticker,
		mrc20Id:        mrc20Id,
	}
}

func normaliseDisplayCurrency(value string) string {
	currency := strings.ToUpper(strings.TrimSpace(value))
	switch currency {
	case "MVC", "MICROVISIONCHAIN":
		return "SPACE"
	default:
		return currency
	}
}

func normaliseSettlementKind(value string, currency string, mrc20Ticker string, mrc20Id string) string {
	raw := strings.ToLower(strings.TrimSpace(value))
	if raw == "mrc20" || strings.HasSuffix(currency, "-MRC20") || currency == "MRC20" || strings.TrimSpace(mrc20Ticker) != "" || strings.TrimSpace(mrc20Id) != "" {
		return "mrc20"
	}
	if raw == "fiat" {
		return "fiat"
	}
	return "native"
}

func defaultPaymentChain(settlementKind string, currency string) string {
	switch settlementKind {
	case "mrc20":
		return "btc"
	case "fiat":
		return ""
	}
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "BTC":
		return "btc"
	case "DOGE":
		return "doge"
	case "OPCAT":
		return "opcat"
	case "SPACE", "MVC", "MICROVISIONCHAIN":
		return "mvc"
	default:
		return ""
	}
}

func isPositivePrice(value string) bool {
	price, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return err == nil && price > 0
}
