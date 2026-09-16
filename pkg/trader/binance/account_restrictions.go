package binance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"sis/pkg/trader"
)

// APIRestrictions is the subset of Binance's /sapi/v1/account/apiRestrictions response
// this package cares about. Field names and JSON tags match Binance's documented response
// exactly. Notably absent: an IP whitelist — Binance's API never exposes the actual list,
// only whether IP restriction is enabled (IPRestrict) and when the key's trading authority
// expires (TradingAuthorityExpirationTime, 0 if it never expires).
type APIRestrictions struct {
	IPRestrict                     bool  `json:"ipRestrict"`
	CreateTime                     int64 `json:"createTime"`
	EnableWithdrawals              bool  `json:"enableWithdrawals"`
	EnableInternalTransfer         bool  `json:"enableInternalTransfer"`
	EnableFutures                  bool  `json:"enableFutures"`
	EnableReading                  bool  `json:"enableReading"`
	EnableSpotAndMarginTrading     bool  `json:"enableSpotAndMarginTrading"`
	TradingAuthorityExpirationTime int64 `json:"tradingAuthorityExpirationTime"`
}

// QueryAPIRestrictions returns the permissions and restrictions of the given API key,
// Binance's equivalent of pkg/trader/bybit.go's QueryAPI — used by VerifyAccount's
// "Проверить" test button when adding a Binance account. Deliberately uses an
// UNRESTRICTED-proxy caller convention: like Bybit's QueryAPI (see VerifyAccount's own
// comment), this exists to discover account state, so it must not be constrained by a
// possibly-stale/wrong previously-stored whitelist — callers should pass creds with
// WhitelistedIPs left empty, same discipline as VerifyAccount already uses for Bybit.
func QueryAPIRestrictions(ctx context.Context, creds trader.Credentials) (APIRestrictions, error) {
	data, err := doRequestToBase(ctx, binanceMainBase, http.MethodGet, "/sapi/v1/account/apiRestrictions", signParams(creds, url.Values{}), creds, creds.APIKey, false)
	if err != nil {
		return APIRestrictions{}, err
	}
	var r APIRestrictions
	if err := json.Unmarshal(data, &r); err != nil {
		return APIRestrictions{}, err
	}
	return r, nil
}
