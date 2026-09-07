# Двусторонняя синхронизация настроек «бот ↔ открытая стратегия»

## Проблема

Сейчас у настроек есть два независимых места редактирования:

- **Бот** (`bots.strategy_config`) — шаблон, применяется к новым сделкам бота.
- **Открытая стратегия** (`strategies`) — конкретная сделка/позиция.

Правка настроек стратегии через `PATCH /strategies/{id}` уже применяется мгновенно
к этой стратегии (`RestartCycle`/`UpdateTPSL`), но никак не влияет на бот — при
следующем изменении настроек бота (`PATCH /bots/{id}` со `strategyConfig`)
`syncBotStrategies` безусловно перезаписывает настройки **всех** активных/finishing
стратегий этого бота их значениями из шаблона, включая уже вручную
кастомизированные — то есть точечная правка стратегии молча стирается следующим
редактированием бота, без предупреждения.

Обратной связи «примени эту правку и в шаблон бота» тоже нет — единственный способ
изменить будущие сделки бота — редактировать сам бот отдельно.

## Решение

Два независимых, не пересекающихся потока синхронизации, оба — по явному
подтверждению пользователя (диалог), никогда не молча:

```
Стратегия → Бот          Бот → Стратегии
(точечно, только шаблон)  (явно выбранный каскад)

PATCH /strategies/{id}    PATCH /bots/{id}
  applyToBot?: bool          applyToActive?: bool
        │                          │
        ▼                          ▼
  UPDATE strategies         UPDATE strategies WHERE
  SET <поля>                  bot_id=X AND status IN
  WHERE id=strategyId          ('active','finishing')
        │                          │
   если applyToBot:          если applyToActive:
   UPDATE bots SET             для каждой — RestartCycle
   strategy_config             (если сетка/матрица/плечо/
   (мёрж синхронизируемых      entry_order_type изменились)
   полей поверх текущих)       или UpdateTPSL (если только
                                TP/SL) — сразу
```

Новых элементов интерфейса на карточке стратегии не добавляется — точка входа
к редактированию уже существует: меню «⋮ → Настройки» на любой карточке, включая
созданные ботом (`StrategyCard.tsx`, пункт `Настройки` → `onEdit` → `StrategyModal`).

## Синхронизируемые поля

Пересечение полей `strategies` и `bots.strategy_config` (`botCfgJSON`), то есть то,
что реально описывает «настройки» (не специфично для конкретного символа/направления):

```
grid_levels, grid_active, grid_step_pct, grid_size_usdt,
tp_mode, tp_pct, sl_type, sl_pct, signal_filter,
leverage, margin_type, hedge_mode, entry_order_type,
signal_configs, steps,
trailing_stop_enabled, trailing_activation_pct, trailing_callback_pct,
matrix_levels, matrix_entry_level, safe_zone_pct,
protected_build, matrix_rebuild_on_sl, matrix_rebuild_from_entry, relative_slots
```

Не синхронизируются: `symbol`, `direction`, `category`, `status`, `max_stop_active`
— специфичны для конкретного инстанса стратегии, в шаблоне бота либо не
существуют, либо имеют другой смысл (whitelist символов на стороне бота).

Этот список используется в обе стороны:
- определяет, показывать ли диалог «Применить к боту?» после правки стратегии
  (нужен только если хоть одно поле из списка реально изменилось);
- определяет, показывать ли диалог «Применить к активным стратегиям?» после
  правки бота (нужен только если хоть одно поле из списка изменилось И у бота
  есть строки `strategies` со `status IN ('active','finishing')`).

## Диалоги (тексты утверждены)

**После правки открытой bot-стратегии**, если задето синхронизируемое поле:

> **Применить к боту «{botName}»?**
> Изменённые параметры можно сохранить как настройки по умолчанию для бота —
> тогда новые сделки бота будут открываться с этими же значениями. Другие уже
> открытые стратегии бота это не затронет.
>
> [Нет, только эта стратегия]  **[Да, применить к боту]** ← акцентная кнопка

**После правки настроек бота**, если есть активные/finishing стратегии и задето
синхронизируемое поле:

> **Применить к активным стратегиям?**
> У бота «{botName}» сейчас {N} открытые стратегии. Применить новые настройки
> к ним прямо сейчас (TP/SL и ордера на бирже будут пересчитаны немедленно),
> или сохранить только для новых стратегий, не трогая уже открытые?
>
> [Нет, только новые стратегии]  **[Да, применить сейчас]** ← акцентная кнопка

Для МультиБота (обе ноги сохраняются одной формой `MultiBotForm`) — диалог
показывается **один раз** на весь сейв (если хотя бы у одной из ног есть активные
стратегии), ответ применяется к обеим `PATCH`-заявкам (сигнальной и hedge-ноге).

## Бэкенд

**Strategy → Bot** (`PATCH /strategies/{id}`, тело + `applyToBot?: boolean`):
- После успешного `UPDATE strategies ...` (как сейчас — без изменений в этой части).
- Если `applyToBot=true` — синхронно (один быстрый `UPDATE`, без обращений к бирже)
  мёржим синхронизируемые поля в `bots.strategy_config` этого `bot_id`:
  прочитать текущий `strategy_config`, unmarshal в `botCfgJSON`, перезаписать
  пересекающиеся поля значениями из применённого payload, marshal обратно,
  `UPDATE bots SET strategy_config=$1, updated_at=NOW()`.
- Пишем событие боту через `s.logBotEvent` — «Настройки синхронизированы из
  открытой стратегии {symbol}».
- Остальные активные стратегии бота не трогаем.
- `applyToBot=true` без `bot_id` на стратегии (ручная, не бот-стратегия) —
  игнорируется как no-op (фронт и не должен присылать этот флаг в таком случае,
  но бэкенд не должен падать).

**Bot → Strategies** (`PATCH /bots/{id}`, тело + `applyToActive?: boolean`):
- `syncBotStrategies` сейчас вызывается безусловно при изменении `strategyConfig`
  (`bots_handler.go:664-682`). Меняем: вызывается только если `applyToActive=true`.
  Если `applyToActive` не пришёл или `false` — конфиг бота сохраняется как шаблон
  (как сейчас, шаг с `UPDATE bots SET strategy_config=...` не меняется), но
  `syncBotStrategies` не вызывается вообще — активные/finishing стратегии не
  трогаются ни в БД, ни в памяти движка.
- Внутри `syncBotStrategies`, после `UPDATE ... RETURNING id`, для каждого id —
  как в ручной правке одной стратегии (`strategy_handler.go:545-582`) — решаем
  `RestartCycle` (если задело сетку/матрицу/плечо/`entry_order_type`) или
  `UpdateTPSL` (если только TP/SL). «Структурность» изменения определяем сравнением
  нового `cfg` с `oldCfg` бота (он уже вычитывается в начале `PatchBot` — сейчас
  используется только в guard'е смены `strategy_type`, нужно поднять чтение выше,
  чтобы было доступно всегда при изменении `strategyConfig`).
- Остаётся fire-and-forget goroutine, как сейчас (`go s.syncBotStrategies(...)`) —
  HTTP-ответ не блокируется на пересчёте TP/SL по бирже для N стратегий.

## Фронтенд

- `StrategyModal.tsx` (~строка 401, `handleSubmit`): перед `updateStrategy`
  сравнить `payload` с исходным `strategy` по списку синхронизируемых полей;
  если есть diff и `strategy.bot_id` задан — показать модалку (по образцу
  `ResetStatsConfirmModal`), дождаться ответа, вызвать
  `updateStrategy(strategy.id, { ...payload, applyToBot })`.
- `BotForm.tsx` (~строка 461, `handleSubmit`): по аналогии с существующими
  `showResetStatsConfirm`/`showCoinFilterConfirm` добавить `showApplyActiveConfirm`
  — если у бота (или хотя бы одной ноги МультиБота) есть активные/finishing
  стратегии и меняется синхронизируемое поле, показать диалог перед `doSubmit()`,
  прокинуть `applyToActive` в payload обеих PATCH-заявок при МультиБоте.
- Новых визуальных элементов на карточке стратегии не добавляется — точка входа
  к редактированию уже существующая (меню «⋮ → Настройки»).

## Затрагиваемые механики

- `services/api-gateway/strategy_handler.go` (`PatchStrategy`) — новый опциональный
  флаг `applyToBot`, остальная логика (`RestartCycle`/`UpdateTPSL` для самой
  стратегии) не меняется.
- `services/api-gateway/bots_handler.go` (`PatchBot`, `syncBotStrategies`) — новый
  опциональный флаг `applyToActive`; **меняется существующее поведение**:
  `syncBotStrategies` перестаёт быть безусловной при изменении `strategyConfig`.
- `pkg/strategy` (`Engine.RestartCycle`, `Engine.UpdateTPSL`) — вызываются из
  нового места (`syncBotStrategies`), сами не меняются.
- Фронтенд: `StrategyModal.tsx`, `BotForm.tsx`, `HedgeBotForm.tsx`/`MultiBotForm.tsx`.

## Тест-ревью

- `go test ./services/api-gateway/... ./pkg/strategy/...` — включая существующие
  `bots_handler_test.go`.
- Новые regression-тесты (бэкенд):
  - `applyToBot=true` мёржит только синхронизируемые поля в `bots.strategy_config`
    и не трогает остальные строки `strategies` этого бота.
  - `applyToBot` отсутствует/`false` — поведение как сейчас, `bots.strategy_config`
    не меняется.
  - `applyToActive=false` (или отсутствует) при изменении `strategyConfig` —
    `syncBotStrategies` не вызывается, активные стратегии не меняются ни в БД,
    ни в памяти (регресс-тест на новое поведение, отличное от текущего).
  - `applyToActive=true` — `RestartCycle` вызывается при структурных изменениях,
    `UpdateTPSL` — при изменении только TP/SL.
- Фронтовые тесты (vitest) на новые confirm-модалки — по образцу
  `HedgeBotForm.embeddedResetConfirm.test.tsx`.
