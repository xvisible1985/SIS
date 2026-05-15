# Account Page Design

## Overview

A new `/account` page with 4 horizontal tabs: Profile, Billing, Integrations, Referrals. The page lives inside the existing Layout (sidebar + main area), matching the style of other pages in the app.

---

## Layout

- Route: `/account`
- Component: `frontend/src/pages/AccountPage.tsx`
- No padding override needed (uses standard `p-6` from Layout)
- Tab navigation: horizontal tabs at the top of the page content area
- Active tab indicator: bottom border `#5b8cff`, color `#5b8cff`
- Inactive tabs: color `#64748b`
- Content max-width: `480px` for forms, `560px` for billing cards

---

## Tab 1 — Profile

**Sections:**

1. **User card** (read-only): initials avatar, username, email
2. **Username field**: editable input + "Сохранить" button → `PATCH /account/profile { username }`
3. **Email field**: read-only display (email changes not supported)
4. **Change password block**: 3 inputs (current password, new password, confirm new) + "Изменить пароль" button → `POST /account/change-password { current_password, new_password }`

**API:**
- `GET /account/profile` → `{ email, username }`
- `PATCH /account/profile` body: `{ username: string }` → `{ email, username }`
- `POST /account/change-password` body: `{ current_password, new_password }` → `204` or `400 { error }`

**Validation:**
- New password ≥ 8 characters
- Confirm must match new password (client-side only)
- Wrong current password → show error message under the form

---

## Tab 2 — Billing

**Sections:**

1. **Novabot balance card**: shows `$0.00`, "Пополнить" button → navigates to `/billing` (stub)
2. **Current plan block**: plan name from `users.plan`, next payment date (stub — shows `—`), "Сменить план" button (stub)
3. **Transaction history table**: columns — date, type (пополнение/списание), amount, description. Empty state: "Транзакций пока нет"

All data in this tab is UI stubs — no new backend logic required for this tab. Plan name comes from existing `GET /account/profile` response (add `plan` field).

---

## Tab 3 — Integrations

**Sections:**

1. **Telegram connection block**:
   - If not connected: instruction text + "Подключить Telegram" button
   - Button click → `GET /account/telegram-link` → receives `{ url }` → opens the URL in a new tab
   - URL format: `https://t.me/novabot?start=<token>`
   - After user clicks Start in Telegram, the bot calls `POST /account/telegram-verify` with the token
   - Page polls `GET /account/profile` every 3 seconds (up to 30s) to detect when `telegram_username` appears
   - If connected: shows Telegram username + "Отключить" button → `DELETE /account/telegram`

2. **Notification settings** (only visible when Telegram is connected):
   - Toggle: "Уведомление по сделке" → `on_trade`
   - Toggle: "Уведомление по сигналу" → `on_signal`
   - Toggle: "Уведомление по балансу" → `on_balance`
   - Auto-save on toggle → `PATCH /account/notifications`

**API:**
- `GET /account/profile` — add fields: `telegram_username: string | null`
- `GET /account/telegram-link` → `{ url: string, token: string }`
- `POST /account/telegram-verify` body: `{ token: string, chat_id: number, username: string }` → `204` (called by bot, not user)
- `DELETE /account/telegram` → `204`
- `GET /account/notifications` → `{ on_trade, on_signal, on_balance: boolean }`
- `PATCH /account/notifications` body: `{ on_trade?, on_signal?, on_balance?: boolean }` → `204`

---

## Tab 4 — Referrals

**Sections:**

1. **Referral link block**: `https://app.novabot.io/r/<code>` displayed in a read-only input + "Копировать" button (copies to clipboard). Code is generated on first load if it doesn't exist.

2. **Stats row**: "Приглашено: N" · "Начислено бонусов: $X"

3. **Invited users table**: columns — дата регистрации, email (masked: first 2 chars + `***` + domain), статус (зарегистрирован / активный). Empty state: "Приглашённых пока нет"

**API:**
- `GET /account/referral` → `{ code, link, count: number, total_rewards: number, signups: [{ date, email_masked, active }] }`

---

## Database Schema

```sql
-- migrations/017_account_page.sql

CREATE TABLE IF NOT EXISTS telegram_connections (
    user_id     UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    chat_id     BIGINT NOT NULL,
    username    TEXT,
    connected_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS telegram_pending_tokens (
    token       TEXT PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '10 minutes'
);

CREATE TABLE IF NOT EXISTS telegram_notification_settings (
    user_id     UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    on_trade    BOOLEAN NOT NULL DEFAULT TRUE,
    on_signal   BOOLEAN NOT NULL DEFAULT TRUE,
    on_balance  BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE IF NOT EXISTS referral_codes (
    user_id     UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    code        TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS referral_signups (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    referrer_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    referee_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    rewarded    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(referee_id)
);
```

---

## Backend — File Structure

**New files:**
- `services/api-gateway/account_handler.go` — all `/account/*` endpoints
- `migrations/017_account_page.sql`

**Modified files:**
- `services/api-gateway/server.go` — register `/account/*` routes
- `services/api-gateway/main.go` — no changes needed (routes go through server)

---

## Frontend — File Structure

**New files:**
- `frontend/src/pages/AccountPage.tsx` — main page + tab routing
- `frontend/src/api/account.ts` — all API calls for account endpoints

**Modified files:**
- `frontend/src/App.tsx` — add `/account` route
- `frontend/src/components/Layout.tsx` — no change needed
- `frontend/src/components/Sidebar.tsx` — add Account link (via `onOpenSettings` or new nav item)

---

## Error Handling

- Profile save / password change: show inline error message below the form
- Telegram link generation failure: show toast error
- All API calls: show generic error toast on 5xx

---

## Non-Goals (out of scope)

- Actual billing / payment processing
- Email change
- Two-factor authentication
- Telegram bot implementation (only the token-exchange endpoint on the web API side)
- Subscription upgrade flow (buttons are stubs)
