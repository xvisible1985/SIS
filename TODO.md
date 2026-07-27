# SIS — Отложенные задачи (Backlog)

Файл для фиксации задач, которые сознательно отложены и к которым нужно вернуться.

---

## PositionsStream: не путать «аккаунт не найден» с ошибкой БД

**Статус:** ⏳ Ожидает
**Приоритет:** Низкий-средний — не баг с потерей данных, но искажает диагностику при сбоях БД.

### Проблема

`services/api-gateway/trader_ws_handler.go` (`PositionsStream`, GET `/ws/trader/positions`) при проверке владения аккаунтом (`SELECT ... FROM exchange_accounts ...`) не различает «строк нет» (`pgx.ErrNoRows`, аккаунт реально не найден/не принадлежит юзеру) и «запрос не выполнился» (обрыв/таймаут соединения с БД через pgbouncer). В обоих случаях сейчас отдаётся `404 "account not found"`.

Обнаружено 2026-07-26 при разборе инцидента: кратковременный обрыв TLS-хендшейка до pgbouncer (`tls error: EOF`) привёл к тому, что WS-стрим позиций стал отвечать 404, что в логе выглядело как «аккаунта нет», хотя реальная причина — недоступность БД. Затруднило диагностику.

**Важно:** фронтенд (`usePositionsWs.ts`) и так переподключается через 5с при любом обрыве WS вне зависимости от кода ответа (браузерный WebSocket API не отдаёт JS код HTTP-ответа на неудавшийся handshake) — то есть фикс не меняет поведение автопереподключения, только корректность семантики ошибок/логов.

### Что нужно сделать

1. В `PositionsStream` (`services/api-gateway/trader_ws_handler.go:32-47`) после каждого из двух `QueryRow` (обычный и admin-фолбэк) проверять `errors.Is(err, pgx.ErrNoRows)`:
   - Если ошибка НЕ `ErrNoRows` — логировать (`log.Printf("trader ws: query account %s: %v", ...)`) и отдавать `500`, не пытаясь admin-фолбэк.
   - `404` оставлять только когда обе попытки закончились именно `ErrNoRows`.
2. Паттерн `errors.Is(err, pgx.ErrNoRows)` уже используется в `telegram_auth_handler.go`, `bot_engine.go`, `hedge_engine.go` — взять оттуда.
3. Добавить юнит-тест на различение статусов по типу ошибки БД.
4. Прогнать `go test ./services/api-gateway/...`.

### Связь с текущей архитектурой

Затрагивает только `services/api-gateway/trader_ws_handler.go`. `pkg/trader.RunPositionStream`, `s.isAdmin`, остальные сервисы — не трогаются.

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

---

## Matrix repair-pass: нет backoff на повторные неудачи + объём логов

**Статус:** ⏳ Ожидает
**Приоритет:** Низкий — не баг с потерей данных, унаследовано от уже существующего поведения нового-пары-цикла, не новый риск.

### Проблема

Найдено code-quality ревью repair-прохода (`docs/superpowers/plans/2026-07-24-matrix-slot-repair-priority.md`, Task 2, коммит `299a69e`, `services/api-gateway/matrix_engine.go`):

1. **Нет cooldown/backoff.** Если `createBotStrategy` для repair-кандидата стабильно падает (делистнутый символ, биржа отклоняет ордер и т.п.), символ будет пересчитываться и повторно пытаться открыться на КАЖДОМ тике бесконечно — repair-проход обходит activation-signal gate, так что символы без подтверждающего сигнала теперь тоже ретраятся каждый тик (раньше это гейтилось).
2. **Объём логов.** `logBotEvent` пишет по каждой попытке (успех/неудача) без дедупликации — для хронически «залипших» символов (ровно тот инцидент, который чинит эта задача — AKEUSDT/DEXEUSDT/VELVETUSDT) это ~8640 строк/день на символ при 30с тике.

Оба пункта — не новый паттерн (уже существующий новый-пары-цикл ведёт себя так же), просто repair теперь расширяет его на символы без сигнала.

### Что нужно сделать (когда дойдут руки)

1. Добавить cooldown/backoff для repair-кандидата после N неудачных попыток подряд — например, по аналогии с существующим 2-минутным grace period у `checkMatrixZombieStrategies`.
2. Рассмотреть дедупликацию/rate-limit в `logBotEvent` для повторяющихся событий по одному и тому же symbol+direction, а не только для repair-прохода — это общий паттерн файла.

### Связь с текущей архитектурой

Затрагивает только `services/api-gateway/matrix_engine.go` (repair-проход и, при желании, `logBotEvent`). Не влияет на существующие тесты — оба ревью (spec-compliance и code-quality) подтвердили: некритично, не блокирует.
