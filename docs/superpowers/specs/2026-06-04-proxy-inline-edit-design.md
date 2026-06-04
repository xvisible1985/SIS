# Proxy Inline Edit Design

**Date:** 2026-06-04  
**Status:** Approved

## Goal

Allow editing all settings of an existing proxy directly in the admin panel table, without navigating away or opening a modal.

## Context

`AdminProxiesTab` shows a table of proxies. Each row supports toggle (enable/disable) and delete. There is no way to edit `host`, `port`, `protocol`, `username`, `password`, or `weight` after creation.

The backend `PATCH /admin/proxies/:id` already accepts all fields. The frontend `updateProxy()` API function already exists. Only the UI is missing.

## Architecture

Single-file change: `frontend/src/features/admin-proxies/AdminProxiesTab.tsx`.

No backend changes. No API changes. No new files.

## Component Design

### ProxyRow — new `editing` state

```
editing: boolean  (default false)
editForm: { protocol, host, port, username, password, weight }  (initialized from proxy on edit open)
saving: boolean  (default false)
```

### View mode (editing = false)

Current layout + new **edit button** (pencil icon, `Pencil` from lucide-react) in the actions column, before the toggle button.

### Edit mode (editing = true)

The `<tr>` renders a single `<td colSpan={8}>` containing an inline form with:

| Field      | Input type | Notes                                         |
|------------|-----------|-----------------------------------------------|
| Protocol   | select     | options: `http`, `https`                      |
| Host       | text       | required                                      |
| Port       | number     | required, 1–65535                             |
| Username   | text       | optional, clearable                           |
| Password   | password   | placeholder "оставьте пустым — без изменений" |
| Weight     | number     | min 1                                         |

Buttons: **Сохранить** (primary) · **Отмена** (secondary)

### Save logic

```
password field empty  → do NOT send password key (keep existing)
password field filled → send password key with new value
```

All buttons disabled while `saving = true`.

On success: `onRefresh()`, `editing = false`.

### Styling

Consistent with the existing add-form at the top: `border-slate-700 bg-slate-800` inputs, same label/input sizing. Form appears as a compact horizontal row of fields (flex-wrap, gap-3), same pattern as the create form.

## Testing

Manual:
1. Open proxy table, click ✎ on a row → form appears pre-filled
2. Change host/port, save → table refreshes with new values
3. Leave password blank, save → password unchanged on backend
4. Enter new password, save → password updated
5. Click Cancel → returns to view mode, no changes
6. Empty host, save → browser validation prevents submission
