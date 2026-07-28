-- migrations/084_exchange_account_whitelisted_ips.sql
-- Кэш IP-whitelist API-ключа аккаунта (Bybit GET /v5/user/query-api, поле ips[]),
-- используется для маршрутизации запросов только через прокси, чьи IP в этом списке.
-- NULL/пустой массив = ограничений на бирже нет (текущее поведение без изменений).
-- См. docs/superpowers/specs/2026-07-28-proxy-whitelist-routing-design.md.
ALTER TABLE exchange_accounts ADD COLUMN IF NOT EXISTS whitelisted_ips TEXT[];
