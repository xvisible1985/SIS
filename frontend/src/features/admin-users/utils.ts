import type { AdminUser } from './types';

export const initials = (name: string) =>
  name.split(' ').map((s) => s[0]).slice(0, 2).join('').toUpperCase();

export const fmtDate = (d: Date) =>
  d.toLocaleDateString('ru-RU', { day: '2-digit', month: 'short', year: 'numeric' });

export const fmtDateTime = (d: Date) =>
  d.toLocaleDateString('ru-RU', { day: '2-digit', month: 'short' }) +
  ' · ' +
  d.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' });

export const fmtMoney = (n: number) =>
  '$' + n.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 });

export const daysSince = (d: Date) =>
  Math.floor((Date.now() - d.getTime()) / 86400000);

export function maskKey(value: string): string {
  if (value.length <= 8) return value;
  return value.slice(0, 4) + '·'.repeat(Math.max(4, value.length - 8)) + value.slice(-4);
}

export function findUser(users: AdminUser[], id: string | null | undefined) {
  if (!id) return undefined;
  return users.find((u) => u.id === id);
}

/** Простой генератор паролей — латиница + цифры + спецсимволы, без похожих символов */
export function generatePassword(length = 14): string {
  const chars = 'ABCDEFGHJKMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789!@#$';
  let out = '';
  for (let i = 0; i < length; i++) {
    out += chars[Math.floor(Math.random() * chars.length)];
  }
  return out;
}

/** Цвет аватара из хэша имени — детерминированный */
export function avatarGradient(name: string): string {
  const hash = name.split('').reduce((a, c) => a + c.charCodeAt(0), 0);
  const hues = [220, 260, 340, 30, 160, 200];
  const h = hues[hash % hues.length];
  return `linear-gradient(135deg, hsl(${h} 70% 55%), hsl(${(h + 50) % 360} 70% 50%))`;
}

/** Брендовые градиенты для биржевых плашек */
export const EXCHANGE_STYLES: Record<string, { bg: string; fg: string; abbr: string }> = {
  Bybit:   { bg: 'linear-gradient(135deg,#f7a600,#e88f00)', fg: '#1a1100', abbr: 'BYB' },
  Binance: { bg: 'linear-gradient(135deg,#f3ba2f,#daa520)', fg: '#1a1100', abbr: 'BIN' },
  OKX:     { bg: 'linear-gradient(135deg,#2a2a2a,#525252)', fg: '#ffffff', abbr: 'OKX' },
};

export function exchangeStyle(name: string) {
  return EXCHANGE_STYLES[name] ?? { bg: 'rgba(255,255,255,.06)', fg: '#cfd5e1', abbr: name.slice(0, 3).toUpperCase() };
}
