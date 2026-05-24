# SIS — Отложенные задачи (Backlog)

Файл для фиксации задач, которые сознательно отложены и к которым нужно вернуться.

---

## Phase 2: WebSocket Proxy Support

**Статус:** ⏳ Ожидает  
**Зависимость:** Завершение Phase 1 (REST proxy load balancing) — `pkg/proxy` уже готов.  
**Приоритет:** Средний — REST proxy решает основную часть rate-limit и IP-ротации.

### Проблема

Сейчас вся HTTP-логика (REST API) идёт через балансируемый пул прокси (`pkg/proxy`), а WebSocket-соединения с биржами (`pkg/trader`) идут напрямую с IP сервера (`websocket.DefaultDialer`).

Это создаёт:
1. **Разные IP для REST и WS** — биржа видит ордера с прокси, а стримы позиций — с другого IP. Флаги безопасности.
2. **Rate limit / бан по IP на WS** — Bybit лимитирует количество WS-соединений с одного IP.
3. **Обход прокси-логики** — если прокси используются для гео-обхода или IP-ротации, WS мимо прокси эту логику обходит.

### Что нужно сделать

1. **Доработать `pkg/proxy/client.go`** — добавить функцию `WSDialer()` которая возвращает `*websocket.Dialer` с `Proxy = http.ProxyURL(...)` на основе `Manager.Pick()`.
2. **Заменить `websocket.DefaultDialer` в:**
   - `pkg/trader/trade_ws.go` — `TradeStream.connect()`
   - `pkg/trader/private_stream.go` — `RunPrivateStream()`
   - `pkg/signal/hub.go` — публичные WS-подписки (если есть)
3. **Health-check для WS** — опционально: проверять не только REST HEAD, но и WS upgrade через прокси.
4. **Тестирование** — нужен реальный прокси с поддержкой HTTP CONNECT и живой WS endpoint Bybit/Binance.

### Техническая деталь

`gorilla/websocket` поддерживает прокси нативно:

```go
dialer := &websocket.Dialer{
    Proxy: http.ProxyURL(proxyURL),
}
dialer.DialContext(ctx, "wss://stream.bybit.com/v5/trade", nil)
```

Прокси должен поддерживать `CONNECT` на 443 порт (HTTP CONNECT туннель для TLS).

### Связь с текущей архитектурой

- `pkg/proxy.Manager.Pick()` уже выбирает лучший прокси.
- `pkg/proxy.Proxy.URL` уже содержит `*url.URL` с авторизацией.
- Остаётся только обернуть это в `websocket.Dialer` и интегрировать в 2–3 call sites.
