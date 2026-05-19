# Admin Users Page — Design Spec
**Date:** 2026-05-19
**Status:** Approved

---

## Overview

Новая вкладка «Пользователи» в существующей AdminPage для управления пользователями платформы. Split-панель: список пользователей слева с поиском и фильтрами, панель деталей и редактирования справа.

---

## Подход

**Approach A:** одна миграция + новый `admin_users_handler.go` + фича `src/features/admin-users/` из Claude Design handoff.

---

## База данных

### Миграция `021_user_management.sql`

Новые колонки в таблице `users`:

```sql
role            TEXT         NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin'))
is_curator      BOOLEAN      NOT NULL DEFAULT FALSE
is_blocked      BOOLEAN      NOT NULL DEFAULT FALSE
email_verified  BOOLEAN      NOT NULL DEFAULT FALSE
referrer_id     UUID         REFERENCES users(id) ON DELETE SET NULL
novabot_balance NUMERIC(18,8) NOT NULL DEFAULT 0
```

Новая таблица `novabot_transactions`:

```sql
CREATE TABLE novabot_transactions (
    id         UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    admin_id   UUID         REFERENCES users(id) ON DELETE SET NULL,
    amount     NUMERIC(18,8) NOT NULL,   -- положительное = начисление, отрицательное = списание
    note       TEXT         NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE INDEX novabot_transactions_user ON novabot_transactions (user_id, created_at DESC);
```

---

## Backend API

### Файл `services/api-gateway/admin_users_handler.go`

Все роуты под `RequireAuth + RequireAdmin`.

| Метод    | Путь                                    | Действие                          |
|----------|-----------------------------------------|-----------------------------------|
| `GET`    | `/admin/users`                          | Список пользователей + accounts   |
| `PATCH`  | `/admin/users/{id}`                     | Обновить role / curator / refererId |
| `POST`   | `/admin/users/{id}/email/verify`        | Подтвердить email вручную         |
| `POST`   | `/admin/users/{id}/password`            | Сменить пароль пользователя       |
| `POST`   | `/admin/users/{id}/email/resend`        | Переотправить письмо верификации (заглушка — возвращает 200, письма нет) |
| `POST`   | `/admin/users/{id}/email/reset`         | Сбросить верификацию email        |
| `POST`   | `/admin/users/{id}/balance/adjust`      | Начислить/списать novabot         |
| `POST`   | `/admin/users/{id}/block`               | Заблокировать (мягко)             |
| `POST`   | `/admin/users/{id}/unblock`             | Разблокировать                    |
| `DELETE` | `/admin/users/{id}/accounts/{aid}`      | Удалить API-ключ                  |

### GET /admin/users — структура ответа

```json
[
  {
    "id": "uuid",
    "email": "user@example.com",
    "name": "user@example.com",
    "role": "user",
    "curator": false,
    "status": "active",
    "balance": 0.0,
    "joined": "2026-04-16T00:00:00Z",
    "lastActive": null,
    "emailVerified": false,
    "refererId": null,
    "blockReason": null,
    "accounts": [
      {
        "id": "uuid",
        "exchange": "Bybit",
        "label": "main",
        "apiKey": "decrypted-full-key",
        "perms": [],
        "added": "2026-04-17T00:00:00Z"
        // perms всегда [] — в БД права по ключу не хранятся
      }
    ]
  }
]
```

Поле `name` = `email` (поле name в БД отсутствует, используем email как отображаемое имя).  
Поле `apiKey` — бэкенд расшифровывает через `encKey` перед отдачей (endpoint защищён `RequireAdmin`).  
Поле `status` вычисляется: `is_blocked=true` → `"blocked"`, `email_verified=false` → `"pending"`, иначе `"active"`.

### PATCH /admin/users/{id}

Тело может содержать любое из полей: `role`, `curator`, `refererId` (null = отвязать).

### POST /admin/users/{id}/balance/adjust

```json
{ "amount": 50.0, "note": "Бонус за реферала" }
```

Атомарно: обновляет `users.novabot_balance` и вставляет запись в `novabot_transactions`. `admin_id` берётся из JWT контекста.

### POST /admin/users/{id}/block

```json
{ "reason": "Нарушение правил" }
```

Мягкая блокировка: устанавливает `is_blocked=true`. Существующие JWT продолжают работать до истечения (24ч). Новые логины получают `403 Forbidden`.

### Изменение RequireAdmin

Вместо проверки `adminEmails` (env) — запрос в БД:

```sql
SELECT role FROM users WHERE id = $1
```

Если `role = 'admin'` — доступ разрешён.

### Bootstrap при старте (main.go)

При инициализации сервера: для каждого email из `ADMIN_EMAILS` выполнить:

```sql
UPDATE users SET role = 'admin' WHERE email = $1 AND role != 'admin'
```

Это позволяет плавно мигрировать без потери доступа существующих администраторов.

### Изменение Login (auth_handler.go)

Добавить проверку `is_blocked` при логине:

```sql
SELECT id, password_hash, is_blocked FROM users WHERE email = $1
```

Если `is_blocked = true` → `403 {"error": "account blocked"}`.

---

## Frontend

### Структура файлов

```
src/
  features/
    admin-users/
      AdminUsersPage.tsx   ← из Claude Design handoff (без изменений)
      types.ts             ← из handoff (без изменений)
      utils.ts             ← из handoff (без изменений)
      index.ts             ← из handoff (без изменений)
      api.ts               ← новый — хук useAdminUsers
      components/
        Avatar.tsx         ← из handoff
        MaskedKey.tsx      ← из handoff
        RefererPicker.tsx  ← из handoff
        RoleTag.tsx        ← из handoff
        Segmented.tsx      ← из handoff
        StatusPill.tsx     ← из handoff
        Toggle.tsx         ← из handoff
        UserDetailPanel.tsx ← из handoff
        UserList.tsx       ← из handoff
```

### api.ts — хук useAdminUsers

Использует существующий `apiClient` из `api/client.ts` (не react-query, по паттерну проекта):

```ts
function useAdminUsers() {
  const [users, setUsers] = useState<AdminUser[]>([])
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => { ... }, [])
  useEffect(() => { load() }, [load])

  const action = useCallback(async (a: AdminAction) => {
    // switch по a.type → apiClient calls
    await load() // рефетч после действия
  }, [load])

  return { users, loading, action, refresh: load }
}
```

### Интеграция в AdminPage.tsx

Вкладка добавляется первой:

```ts
const TABS = [
  { id: 'users',      label: 'Пользователи' },
  { id: 'monitoring', label: 'Мониторинг'   },
  { id: 'signals',    label: 'Сигналы'      },
] as const
```

Рендер вкладки:

```tsx
{tab === 'users' && (
  <div className="flex flex-1 flex-col overflow-hidden">
    <UsersTab />
  </div>
)}
```

`UsersTab` — небольшой компонент внутри AdminPage, который вызывает `useAdminUsers` и рендерит `<AdminUsersPage>`.

---

## Что НЕ входит в этот спек (оставлено на потом)

- Audit trail (лог действий администратора)
- Экспорт CSV
- Массовые операции
- Виртуализация списка (react-virtuoso) при >500 пользователях
- Модалка «Создать пользователя»
- Поле `name` (отдельная миграция, сейчас используем email)
- Поле `lastActive` (отдельная миграция / Redis)

---

## Резюме изменений

| Слой | Файлы |
|------|-------|
| Миграция | `migrations/021_user_management.sql` |
| Backend — новый | `services/api-gateway/admin_users_handler.go` |
| Backend — изменить | `services/api-gateway/middleware.go` (RequireAdmin) |
| Backend — изменить | `services/api-gateway/auth_handler.go` (Login блокировка) |
| Backend — изменить | `services/api-gateway/main.go` (роуты + bootstrap) |
| Frontend — новые | `src/features/admin-users/**` (handoff + api.ts) |
| Frontend — изменить | `src/pages/AdminPage.tsx` (новая вкладка) |
