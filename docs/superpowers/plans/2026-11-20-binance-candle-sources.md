# Binance candles and exchange-scoped signals

1. In pkg/exchange/binance, reproduce the production Kline JSON decoding failure and protect case-sensitive fields with a regression fixture. Run the exchange package tests.
2. In pkg/signal, keep independent candle and compute engines for Bybit and Binance. Verify that equal symbol/timeframe/config inputs can produce different states without cross-contamination.
3. At strategy and API-gateway account call sites, derive the signal engine from the owning account. Separate periodic and reactive bot routing by exchange. Keep accountless callers on the Bybit engine.
4. Run exchange, signal, strategy, and API-gateway tests. Verify production ingestion separately when releasing.
