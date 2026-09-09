# Тестовый харнес для `pkg/strategy` ↔ `trader.Exchange`

## Проблема

Все существующие тесты в `pkg/strategy`, которые касаются взаимодействия с биржей
(`resolveExchangeAvgEntry`, `matrixUpdateTP`, `updateTP` и т.п.), используют технику
«паника на nil `sr.runner`»: код вызывается с намеренно нулевым раннером, и тест
проверяет, что паника происходит в ожидаемой точке. Это доказывает только «код дошёл
до этой строки» — не то, что **вычисленное значение** (цена TP, тип ордера, направление
триггера) корректно.

Именно в этот класс багов попали три реальных инцидента этой сессии:
- MIRAUSDT (исторический, уже упомянут в комментариях кода) — доверие расчётному VWAP
  вместо биржевого ТВХ закрыло шорт в убыток.
- NIULAIUSDT (2026-09-07) — TP выставлен по устаревшему WS-кэшу ТВХ сразу после фила.
- 1000NEIROCTOUSDT (2026-09-08/09) — тот же баг трижды подряд в одном цикле, плюс
  отдельный (уже пофикшенный commit `af35fde`) баг с ошибочным управляющим уровнем L(0).

Первый из них (устаревший WS-кэш ТВХ) исправлен в текущей сессии
(`pkg/strategy/cycle.go:resolveExchangeAvgEntry`, `pkg/strategy/matrix.go:matrixUpdateTP`)
без юнит-теста — только `go test ./pkg/strategy/...` на регресс существующих тестов,
т.к. подходящей инфраструктуры для мока `trader.Exchange` в пакете не было.

## Решение

### Файлы

- `pkg/strategy/exchange_fake_test.go` — `fakeExchange`, реализующий весь интерфейс
  `trader.Exchange` (12 методов: `PlaceOrder`, `PlaceOrderBatch`, `CancelOrder`,
  `CancelOrderBatch`, `PlaceOrderREST`, `CancelOrderREST`, `CancelAllOrders`,
  `FetchPositions`, `FetchOpenOrdersForSymbolAll`, `FetchClosedPnlForSymbol`,
  `FetchRecentClosedPnl`, `GetWalletBalance`, `SetLeverage`, `SwitchPositionMode`,
  `GetMarkPrice`). Паттерн — тот же, что уже есть в `pkg/trader/exchange_test.go` для
  `fakeWSOrderClient` (канонические ответы/ошибки, без сети, без паник по умолчанию),
  но с двумя расширениями, нужными именно здесь:
  - **лог вызовов** (слайс на каждый метод, а не одно последнее значение) — код вроде
    `matrixUpdateTP` делает cancel-затем-place, и тест должен проверить и факт, и
    порядок, и (для `PlaceOrder`) точные поля запроса (`OrderType`, `TriggerDirection`,
    `TriggerPrice` и т.д.), а не просто «что-то вызвалось»;
  - **очередь ответов** для методов, которые могут быть вызваны несколько раз за один
    тест с разными ожидаемыми результатами (в первую очередь `FetchPositions`) — после
    исчерпания очереди возвращается последний заданный ответ.
  - Безопасен для конкурентного доступа (мьютекс) — на случай если тест когда-нибудь
    задействует реальный worker-луп `sr.startWorker()`, а не только синхronные вызовы
    напрямую.

- `pkg/strategy/testrunner_test.go` — хелперы сборки `*StrategyRunner`/`*AccountRunner`
  с `fakeExchange` и предзаданным состоянием WS-кэша (`positions`/`posAvgEntry` в
  `AccountRunner`, доступны напрямую как приватные поля пакета). Цель — не плодить
  ручную сборку `&StrategyRunner{runner: &AccountRunner{...}}` в каждом тесте.

- `pkg/strategy/resolve_avg_entry_test.go` — регресс-тесты на устаревший WS-кэш ТВХ:
  - `wsQty < computedQty` → `resolveExchangeAvgEntry` игнорирует WS-кэш, вызывает
    `FetchPositions` через `fakeExchange`, использует полученное значение.
  - `wsQty >= computedQty` → доверяет WS-кэшу, `FetchPositions` не вызывается вообще
    (проверяется по логу вызовов `fakeExchange`).

- `pkg/strategy/matrix_tp_order_type_test.go` — регресс-тесты на выбор Limit/Stop для
  TP в `matrixUpdateTP`:
  - цена ещё не дошла до расчётного TP → `PlaceOrder` с `OrderType=Limit`, `Price` равен
    расчётному TP.
  - цена уже прошла TP (сценарий «резкий разворот, TP-лимитка мгновенно исполнилась бы»)
    → `PlaceOrder` с `OrderType=Market`, `OrderFilter=StopOrder`, `TriggerPrice` равен
    расчётному TP, `TriggerDirection` соответствует направлению позиции (1 для шорта,
    2 для лонга — см. существующий комментарий в `matrix.go`).

### Что не входит в эту задачу

- Тесты на уже исправленный (и уже покрытый в `engine_test.go`) баг с управляющим
  уровнем L(0) — не трогаем, дублирование не нужно.
- `updateTrailingStop` (отдельный путь без `resolveExchangeAvgEntry`) — нет
  подтверждённого инцидента, вне текущего фокуса.
- Расширение `fakeExchange` до отдельного экспортируемого пакета — пока единственный
  потребитель `pkg/strategy`, следуем тому же паттерну, что `pkg/trader`
  (`_test.go`-only, package-private).

## Тест-ревью

- `go build ./...`, `go vet ./...` — чисто.
- `go test ./pkg/strategy/...` — все существующие тесты пакета остаются зелёными
  (включая четыре теста, которые сейчас доходят до `resolveExchangeAvgEntry` через
  nil-runner-панику — `fakeExchange`/хелперы не должны их сломать, это дополнение,
  не замена).
- Новые тесты (`resolve_avg_entry_test.go`, `matrix_tp_order_type_test.go`) проходят и
  реально проверяют поведение (конкретные значения полей запроса к бирже), а не факт
  вызова.
