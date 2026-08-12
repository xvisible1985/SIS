# Дизайн: маршрутизация запросов к бирже с учётом IP-whitelist аккаунта

Дата: 2026-07-28

## Контекст и проблема

Обнаружено 2026-07-27 при разборе продакшн-логов: `syncer: fetch executions account=fda23ac0-... : bybit: retCode=10010: Unmatched IP, please check your API key's bound IP addresses`. Причина — архитектурная, не случайный сбой:

- Все REST-запросы к Bybit (`pkg/trader/bybit.go`, `doSignedGET`/`doSignedPOST`) идут через `proxy.HTTPClient()`, который под капотом на **каждый запрос независимо** выбирает наименее загруженный прокси из общего пула (`Manager.Pick()`, `pkg/proxy/manager.go:103`) — без привязки к аккаунту.
- Если у Bybit API-ключа аккаунта включено ограничение по IP (whitelist), а наш пул из нескольких прокси-IP роутит запросы этого аккаунта то через один IP, то через другой, — часть (или все) запросы будут систематически падать с `retCode=10010`, в зависимости от того, сколько из наших прокси-IP реально в этом whitelist.
- Подтверждено на живом аккаунте: `trader_executions` показывает как успешные, так и упавшие попытки синка подряд — то есть проблема не «оба прокси не подходят», а «только один из двух подходит» (наблюдалось 2 активных прокси: `89.232.177.34`, `91.224.86.8`).
- То же самое затрагивает **размещение реальных ордеров** (`doSignedPOST` → `PlaceOrder`) — не только фоновую синхронизацию истории.

Дополнительно: приватные WebSocket-соединения (`pkg/trader/ws.go`, `private_stream.go`, `trade_ws.go`) и публичные WS-подписки на рыночные данные (`pkg/signal/hub.go`, `ticker_hub.go`) сейчас вообще идут напрямую через `websocket.DefaultDialer`, минуя прокси-пул — это был отдельный пункт в `TODO.md` («Phase 2: WebSocket Proxy Support»), который эта доработка полностью поглощает.

**У Bybit есть способ узнать whitelist ключа программно** — `GET /v5/user/query-api`, поле `ips` в ответе (пустой массив = ограничений нет). В кодовой базе это уже обёрнуто в `trader.QueryAPI(ctx, creds)` (`pkg/trader/bybit.go:892`) и уже частично используется в `VerifyAccount`-хендлере (`services/api-gateway/accounts_handler.go:104-154`), который парсит `ips[]` и отдаёт на фронт — но нигде не сохраняет и не использует для маршрутизации.

## Архитектура

### `pkg/proxy` — новые функции

```go
// PickForIPs выбирает прокси так же, как Pick() (наименее загруженный по весу),
// но если allowedIPs непуст — сначала сужает кандидатов до тех, чей p.URL.Hostname()
// входит в allowedIPs. Пустой allowedIPs = поведение как у Pick() без изменений
// (аккаунт без ограничений по IP).
func (m *Manager) PickForIPs(allowedIPs []string) (*Proxy, error)

// ErrNoWhitelistedProxy возвращается PickForIPs, когда allowedIPs непуст, но ни один
// прокси пула ему не соответствует — запрос НЕ отправляется (fail fast), а не уходит
// через первый попавшийся прокси вслепую.
var ErrNoWhitelistedProxy = errors.New("no proxy matches account's IP whitelist")

// HTTPClientFor — как HTTPClient(), но с фильтрацией по whitelist аккаунта.
func HTTPClientFor(accountID string, allowedIPs []string) (*http.Client, error)

// WSDialerFor — как HTTPClientFor, но возвращает *websocket.Dialer с Proxy на выбранный
// прокси (используется gorilla/websocket'ом нативно через http.ProxyURL).
func WSDialerFor(accountID string, allowedIPs []string) (*websocket.Dialer, error)

// WSDialer — безусловный вариант для публичных (без аккаунта/ключей) WS-подписок:
// обычный Pick() по всему пулу, без whitelist-фильтрации. Зеркало HTTPClient().
func WSDialer() *websocket.Dialer
```

`accountID` в сигнатурах `HTTPClientFor`/`WSDialerFor` используется только для диагностики/логирования (какой аккаунт инициировал выбор) — сама фильтрация целиком определяется `allowedIPs`, который вызывающий код обязан передать актуальным.

### `trader.Credentials` — новые поля

```go
type Credentials struct {
    APIKey         string
    SecretKey      string
    AccountID      string   // новое
    WhitelistedIPs []string // новое; nil/пусто = без ограничений
}
```

`Credentials` уже единственная сущность, которая доходит до каждой функции, обращающейся к Bybit (REST и WS) — добавление этих двух полей закрывает потребность протащить контекст аккаунта до точки выбора прокси без изменения сигнатур `FetchExecutions`, `PlaceOrder`, `RunPrivateStream` и т.д.

### Точки переключения на whitelist-aware маршрутизацию

**REST** (`pkg/trader/bybit.go`, `doSignedGET`/`doSignedPOST`, строки ~84 и ~105): вместо `proxy.HTTPClient()` — `proxy.HTTPClientFor(creds.AccountID, creds.WhitelistedIPs)`; при ошибке (`ErrNoWhitelistedProxy` или сетевая) — не отправлять запрос, вернуть ошибку вызывающему коду как обычно.

**Приватные WS** (используют `creds`, уже есть auth):
- `pkg/trader/ws.go:43`
- `pkg/trader/private_stream.go:79`
- `pkg/trader/trade_ws.go:71`

Во всех трёх — замена `websocket.DefaultDialer.DialContext(ctx, url, nil)` на `dialer, err := proxy.WSDialerFor(creds.AccountID, creds.WhitelistedIPs); ...; dialer.DialContext(ctx, url, nil)`. Ошибка обрабатывается тем же путём, что и сегодняшняя ошибка подключения — все три места уже имеют reconnect-цикл с ретраем через 5 секунд (`private_stream.go`) или аналогичный (`ws.go`, `trade_ws.go`) — `ErrNoWhitelistedProxy` просто станет ещё одной причиной, по которой конкретная попытка подключения не удалась, и будет залогирована/повторена как любая другая сетевая ошибка.

**Публичные WS** (без аккаунта, рыночные данные):
- `pkg/signal/hub.go:206`
- `pkg/signal/ticker_hub.go:166`

Замена `websocket.DefaultDialer.DialContext(...)` на `proxy.WSDialer().DialContext(...)` — без whitelist (нет ключа, нечего сверять), просто равномерное распределение нагрузки по пулу прокси, как и было задумано в исходном пункте `TODO.md` (анти-бан/rate-limit по IP на публичных подписках).

## Модель данных

Миграция (следующий свободный номер после `084_rescue_bot.sql`):

```sql
ALTER TABLE exchange_accounts ADD COLUMN IF NOT EXISTS whitelisted_ips TEXT[];
```

`NULL`/пустой массив = ограничений на бирже нет (текущее поведение без изменений).

## Механика обновления whitelist

1. **`Syncer.runAccount`** (`pkg/trader/syncer.go:99-129`) получает новый тикер (по аналогии с существующими `execTicker`/`histTicker`, интервал — раз в 5 минут, можно синхронизировать с уже существующим `histTicker`, чтобы не плодить лишний тикер): вызывает `trader.QueryAPI(ctx, creds)`, парсит `ips[]` из ответа, обновляет `exchange_accounts.whitelisted_ips`. Также сразу после обновления — обновляет `creds.WhitelistedIPs` в памяти для последующих вызовов в рамках этого же цикла `runAccount`.
2. **`VerifyAccount`** (`services/api-gateway/accounts_handler.go:104-154`) — при ручной проверке аккаунта пользователем уже вызывает `trader.QueryAPI` и парсит `ips[]`; добавляется `UPDATE exchange_accounts SET whitelisted_ips=$1 WHERE id=$2` рядом с уже существующим обновлением `expires_at`.
3. **Каждое место, конструирующее `trader.Credentials{}`** (~44 вхождений в 15 файлах — `pkg/strategy/engine.go`, `startup_reconcile.go`, `services/api-gateway/bot_engine.go`, `hedge_engine.go`, `matrix_engine.go`, `accounts_handler.go`, `trader_handler.go`, `trader_ws_handler.go`, `closed_pnl_syncer.go` и т.д.) — рядом с уже существующим чтением/расшифровкой `api_key_enc`/`secret_enc` из `exchange_accounts` добавляется чтение `id` (для `AccountID`) и `whitelisted_ips` (для `WhitelistedIPs`) из той же строки — эти данные уже выбираются в подавляющем большинстве случаев (accountID почти everywhere уже есть в скоупе как `a.id`/`accountID`/аналог).

## Обработка ошибок

- `PickForIPs` с непустым `allowedIPs` и без совпадений → `ErrNoWhitelistedProxy`. Запрос не отправляется вообще (fail fast, по решению из брейнсторминга — не пытаться угадать через неподходящий прокси и не тратить впустую rate limit биржи на заведомо обречённые запросы).
- REST: ошибка поднимается обычным Go error-путём до вызывающего кода (`FetchExecutions`, `PlaceOrder` и т.д.) — там уже есть логирование (`log.Printf("syncer: fetch executions ...: %v", err)` и аналоги) и retry на следующем тике/цикле.
- WS: ошибка обрабатывается existing reconnect-циклом (`log.Printf(...); time.Sleep(5s); continue`) — трактуется как обычный сбой подключения, не требует нового паттерна.
- Пул полностью пуст (0 прокси) и `allowedIPs` пуст → поведение как сегодня: прямое соединение (backward-compatible fallback, уже реализовано в `BalancedTransport.RoundTrip`/`HTTPClient()`).
- Пул полностью пуст (0 прокси), но `allowedIPs` непуст (аккаунт ограничен по IP) → тоже `ErrNoWhitelistedProxy` — без прокси нечем гарантировать соответствие whitelist, direct-fallback в этом случае не предлагается (сервер сам может быть не в whitelist).
- v1 не пытается автоматически определить собственный публичный IP сервера как валидного кандидата — если у аккаунта в whitelist только «голый» IP сервера без единого прокси, это не будет обнаружено автоматически (см. «Область» ниже).

## Тестирование

- `pkg/proxy`: юнит-тесты `PickForIPs` — пустой `allowedIPs` (ведёт себя как `Pick()`), одно совпадение, несколько совпадений (least-connections внутри отфильтрованного множества), ноль совпадений (`ErrNoWhitelistedProxy`), пустой пул + непустой `allowedIPs` (тоже `ErrNoWhitelistedProxy`).
- `pkg/proxy`: `HTTPClientFor`/`WSDialerFor` — тонкие обёртки, тестируются через факт делегирования в `PickForIPs` (не дублировать логику выбора).
- `Credentials` round-trip не требует отдельного теста — простое расширение структуры полями без бизнес-логики.
- Обновление `whitelisted_ips` в `Syncer`/`VerifyAccount` — по прецеденту `recordRescuePartialClose` (Task 7 плана RescueBot), это DB-зависимый код без установленного паттерна мокирования в этом пакете; проверяется вручную на dev-БД + существующим `//go:build integration`-тестом, если найдётся подходящий прецедент для `VerifyAccount`-подобных хендлеров.
- Ручная сквозная проверка (после реализации, не автоматизируется): для аккаунта `fda23ac0-...` — убедиться, что `whitelisted_ips` заполнился, и что `syncer: fetch executions` перестал перемежаться ошибками `10010`.

## Область

**Входит:** REST-запросы к Bybit (`doSignedGET`/`doSignedPOST`), приватные WS (`ws.go`, `private_stream.go`, `trade_ws.go`), публичные WS (`pkg/signal/hub.go`, `ticker_hub.go`).

**Не входит:**
- Автоматическое определение прямого IP сервера как кандидата для whitelist-совпадения (см. «Обработка ошибок» — v1 ограничение).
- UI-отображение whitelist/предупреждений оператору о несовпадении (сейчас `VerifyAccount` и так отдаёт `ips[]` на фронт — этого достаточно для v1; полноценный UI-индикатор «прокси не подходит» — отдельная задача при необходимости).
- Ретроактивная миграция для аккаунтов, у которых `whitelisted_ips` ещё не заполнен (будет `NULL` до первого срабатывания тикера в `Syncer` или ручного `VerifyAccount` — в этот промежуток поведение как сегодня, т.е. без изменений, не хуже текущего).

Этот дизайн полностью поглощает пункт «Phase 2: WebSocket Proxy Support» из `TODO.md` — будет убран оттуда после фиксации спеки.

## Открытые вопросы

Нет — оба архитектурных развилки (куда класть whitelist на уровне кода — в `Credentials`; что делать при отсутствии совпадений — fail fast) разрешены в ходе брейнсторминга.
