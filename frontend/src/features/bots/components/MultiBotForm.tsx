import { useRef, useState } from 'react';
import type { ChangeEvent } from 'react';
import { X, Bot as BotIcon, Camera, Trash2, Smile, ToggleLeft, ToggleRight } from 'lucide-react';
import { BotForm, type BotFormHandle } from './BotForm';
import { HedgeBotForm, type HedgeBotFormHandle } from './HedgeBotForm';
import { BotIconPicker } from './BotIconPicker';
import { apiClient } from '../../../api/client';
import { useSelectedAccount } from '../../../contexts/AccountContext';
import type { Bot as BotType } from '../types';

type Props = {
  // Editing an existing Мультибот passes both legs (see migration 092's paired_bot_id).
  // Omitting both creates a new one.
  signalBot?: BotType;
  hedgeBot?: BotType;
  onClose: () => void;
  onSaved: () => void;
};

type Tab = 'basic' | 'signal' | 'hedge';

function compressImage(file: File, maxPx = 300, quality = 0.82): Promise<string> {
  return new Promise((resolve, reject) => {
    const img = new Image();
    const url = URL.createObjectURL(file);
    img.onload = () => {
      URL.revokeObjectURL(url);
      const scale = Math.min(1, maxPx / Math.max(img.width, img.height));
      const w = Math.round(img.width * scale);
      const h = Math.round(img.height * scale);
      const canvas = document.createElement('canvas');
      canvas.width = w; canvas.height = h;
      canvas.getContext('2d')!.drawImage(img, 0, 0, w, h);
      resolve(canvas.toDataURL('image/jpeg', quality));
    };
    img.onerror = reject;
    img.src = url;
  });
}

const inputCls =
  'w-full rounded-lg border border-white/[.08] bg-black/[.25] px-3 py-2 text-[13px] text-slate-200 outline-none transition-colors placeholder:text-slate-500 focus:border-[#5b8cff]/50';

function Field({ label, children, required, hint }: {
  label: string; children: React.ReactNode; required?: boolean; hint?: string;
}) {
  return (
    <div>
      <div className="mb-1.5 flex items-baseline gap-2">
        <label className="block text-[11px] font-bold uppercase tracking-wider text-slate-400">
          {label}{required && <span className="ml-0.5 text-[#5b8cff]">*</span>}
        </label>
        {hint && <span className="text-[10px] text-slate-600">{hint}</span>}
      </div>
      {children}
    </div>
  );
}

export function MultiBotForm({ signalBot, hedgeBot, onClose, onSaved }: Props) {
  const isEdit = !!(signalBot && hedgeBot);
  const { selectedAccountId } = useSelectedAccount();

  const [tab, setTab] = useState<Tab>('basic');
  const [name, setName] = useState(signalBot?.name ?? '');
  const [description, setDescription] = useState(signalBot?.description ?? '');
  const [fullDescription, setFullDescription] = useState(signalBot?.fullDescription ?? '');
  const [avatarUrl, setAvatarUrl] = useState(signalBot?.avatarUrl ?? '');
  const [autoMode, setAutoMode] = useState(signalBot?.autoMode ?? false);
  const [maxStrategies, setMaxStrategies] = useState(signalBot?.maxStrategies ?? 0);
  const [maxLongStrategies, setMaxLongStrategies] = useState(signalBot?.maxLongStrategies ?? 0);
  const [maxShortStrategies, setMaxShortStrategies] = useState(signalBot?.maxShortStrategies ?? 0);
  const [maxMarginUsdt, setMaxMarginUsdt] = useState(signalBot?.maxMarginUsdt ?? 0);
  const [maxSymConsecutiveRuns, setMaxSymConsecutiveRuns] = useState(signalBot?.maxSymConsecutiveRuns ?? 0);

  const [showIconPicker, setShowIconPicker] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  const signalRef = useRef<BotFormHandle>(null);
  const hedgeRef = useRef<HedgeBotFormHandle>(null);

  async function handleAvatarFile(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;
    try {
      setAvatarUrl(await compressImage(file));
    } catch {
      // ignore — user can retry
    }
    e.target.value = '';
  }

  async function handleSave() {
    if (!name.trim()) {
      setTab('basic');
      setSubmitError('Название обязательно');
      return;
    }
    setSubmitting(true);
    setSubmitError(null);
    try {
      // Both legs validate/build their own payload in parallel. Either can come back null —
      // failed validation, or it's showing its own confirm dialog (flagged coin, stats
      // reset) and is waiting for the user; re-clicking Сохранить retries cleanly either way.
      const [signalPayload, hedgePayload] = await Promise.all([
        signalRef.current?.trySubmit() ?? Promise.resolve(null),
        hedgeRef.current?.trySubmit() ?? Promise.resolve(null),
      ]);
      if (!signalPayload || !hedgePayload) {
        setSubmitting(false);
        return;
      }

      const identity = {
        name: name.trim(),
        description: description.trim(),
        fullDescription: fullDescription.trim() || undefined,
        avatarUrl: avatarUrl || undefined,
        isPublic: false, // publishing a Мультибот to the catalog isn't wired up yet
      };

      if (isEdit && signalBot && hedgeBot) {
        await Promise.all([
          apiClient.patch(`/bots/${signalBot.id}`, {
            ...signalPayload, ...identity,
            autoMode, maxStrategies, maxLongStrategies, maxShortStrategies, maxMarginUsdt, maxSymConsecutiveRuns,
          }),
          apiClient.patch(`/bots/${hedgeBot.id}`, {
            ...hedgePayload, ...identity,
          }),
        ]);
      } else {
        await apiClient.post('/bots/multi', {
          ...identity,
          accountId: selectedAccountId,
          symbolWhitelist: signalPayload.symbolWhitelist,
          symbolBlacklist: signalPayload.symbolBlacklist,
          strategyConfig: signalPayload.strategyConfig,
          hedgeStrategyConfig: hedgePayload.strategyConfig,
          maxStrategies, maxLongStrategies, maxShortStrategies, maxMarginUsdt, maxSymConsecutiveRuns,
          autoMode,
        });
      }
      onSaved();
      onClose();
    } catch (e) {
      setSubmitError(e instanceof Error ? e.message : 'Неизвестная ошибка');
    } finally {
      setSubmitting(false);
    }
  }

  const tabs: { id: Tab; label: string }[] = [
    { id: 'basic',  label: 'Основное' },
    { id: 'signal', label: 'Мэйн позиция' },
    { id: 'hedge',  label: 'Хедж позиция' },
  ];

  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center overflow-auto bg-[rgba(8,11,18,.78)] p-6 backdrop-blur">
      <div className="flex max-h-[calc(100vh-48px)] w-full max-w-[680px] flex-col overflow-hidden rounded-[18px] border border-white/[.08] bg-[#0c1018] shadow-[0_32px_80px_-16px_rgba(0,0,0,.7)]">
        {/* header */}
        <div className="flex items-center gap-3 border-b border-white/[.06] px-5 py-4">
          <div className="h-9 w-9 shrink-0 overflow-hidden rounded-[9px] border border-fuchsia-500/30">
            {avatarUrl
              ? <img src={avatarUrl} alt="" className="h-full w-full object-cover" />
              : <div className="flex h-full w-full items-center justify-center bg-fuchsia-500/[.14] text-fuchsia-400"><BotIcon size={16} strokeWidth={2} /></div>
            }
          </div>
          <div className="min-w-0 flex-1">
            <h2 className="font-display text-[16px] font-bold tracking-tight text-slate-50">
              {isEdit ? 'Редактировать Мультибота' : 'Создать Мультибота'}
            </h2>
            <p className="text-[11px] text-slate-400">{isEdit ? signalBot?.name : 'Сигнал + Хедж в одной сущности'}</p>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="flex h-[30px] w-[30px] items-center justify-center rounded-md border border-white/[.08] bg-white/[.04] text-slate-300 hover:bg-white/[.08]"
          >
            <X size={14} />
          </button>
        </div>

        {/* tabs */}
        <div className="flex border-b border-white/[.06] px-5">
          {tabs.map(t => (
            <button
              key={t.id}
              type="button"
              onClick={() => setTab(t.id)}
              className={
                'relative mr-4 py-3 text-[15px] font-semibold transition-colors ' +
                (tab === t.id ? 'text-slate-50' : 'text-slate-400 hover:text-slate-200')
              }
            >
              {t.label}
              {tab === t.id && (
                <span className="absolute bottom-0 left-0 right-0 h-[2px] rounded-full bg-fuchsia-500" />
              )}
            </button>
          ))}
        </div>

        {/* body — all three tabs stay mounted (display:none when inactive) so the embedded
            forms' refs stay attached and their state survives switching tabs. */}
        <div className="relative flex-1 overflow-hidden">
          <div style={{ display: tab === 'basic' ? 'block' : 'none' }} className="h-full overflow-auto p-5">
            <div className="flex flex-col gap-4">
              {/* Avatar upload */}
              <div className="flex items-center gap-4">
                <div className="relative shrink-0 group">
                  <div className="h-[72px] w-[72px] overflow-hidden rounded-[14px] border border-white/[.08] bg-black/[.3]">
                    {avatarUrl
                      ? <img src={avatarUrl} alt="avatar" className="h-full w-full object-cover" />
                      : <div className="flex h-full w-full items-center justify-center text-slate-600">
                          <BotIcon size={26} strokeWidth={1.5} />
                        </div>
                    }
                  </div>
                  <div
                    onClick={() => fileInputRef.current?.click()}
                    className="absolute inset-0 flex cursor-pointer items-center justify-center rounded-[14px] bg-black/55 opacity-0 transition-opacity group-hover:opacity-100"
                  >
                    <Camera size={18} className="text-white" />
                  </div>
                  <input ref={fileInputRef} type="file" accept="image/*" className="hidden" onChange={handleAvatarFile} />
                </div>

                <div className="flex flex-1 flex-col gap-2">
                  <Field label="Название" required>
                    <input
                      type="text"
                      value={name}
                      onChange={e => setName(e.target.value.slice(0, 30))}
                      placeholder="BTC Signal + Hedge"
                      className={inputCls}
                      maxLength={30}
                    />
                  </Field>
                  <div className="relative flex flex-wrap items-center gap-2">
                    <button
                      type="button"
                      onClick={() => fileInputRef.current?.click()}
                      className="inline-flex items-center gap-1.5 rounded-md border border-white/[.08] bg-white/[.04] px-3 py-1 text-[11px] font-semibold text-slate-300 hover:bg-white/[.08]"
                    >
                      <Camera size={11} /> Загрузить
                    </button>
                    <button
                      type="button"
                      onClick={() => setShowIconPicker(v => !v)}
                      className="inline-flex items-center gap-1.5 rounded-md border border-white/[.08] bg-white/[.04] px-3 py-1 text-[11px] font-semibold text-slate-300 hover:bg-white/[.08]"
                    >
                      <Smile size={11} /> Иконки
                    </button>
                    {avatarUrl && (
                      <button
                        type="button"
                        onClick={() => setAvatarUrl('')}
                        className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-[11px] text-rose-400 hover:text-rose-300"
                      >
                        <Trash2 size={11} /> Удалить
                      </button>
                    )}
                    {showIconPicker && (
                      <BotIconPicker value={avatarUrl} onChange={url => setAvatarUrl(url)} onClose={() => setShowIconPicker(false)} />
                    )}
                  </div>
                </div>
              </div>

              <Field label="Краткое описание" hint="Показывается под названием на карточке бота">
                <div className="relative">
                  <textarea
                    value={description}
                    onChange={e => setDescription(e.target.value.slice(0, 120))}
                    placeholder="Сигнальные входы с автоматическим хеджем просадки"
                    rows={2}
                    className={`${inputCls} resize-none`}
                    maxLength={120}
                  />
                  <span className="pointer-events-none absolute right-3 bottom-2 text-[10px] text-slate-600">{description.length}/120</span>
                </div>
              </Field>
              <Field label="Подробное описание" hint="Развёрнутое описание для страницы бота">
                <div className="relative">
                  <textarea
                    value={fullDescription}
                    onChange={e => setFullDescription(e.target.value.slice(0, 2000))}
                    placeholder="Опишите логику работы бота, рекомендуемые рыночные условия, особенности настройки..."
                    rows={4}
                    className={`${inputCls} resize-none`}
                    maxLength={2000}
                  />
                  <span className="pointer-events-none absolute right-3 bottom-2 text-[10px] text-slate-600">{fullDescription.length}/2000</span>
                </div>
              </Field>

              <div className="rounded-lg border border-white/[.06] bg-white/[.02]">
                <div className="flex items-center justify-between px-4 py-3">
                  <div>
                    <div className="text-[13px] font-semibold text-slate-200">Авто-режим</div>
                    <div className="mt-0.5 text-[11px] text-slate-400">
                      Сигнальная нога сама открывает стратегии при срабатывании сигналов — без подтверждения
                    </div>
                  </div>
                  <button type="button" onClick={() => setAutoMode(v => !v)} className="text-slate-400">
                    {autoMode ? <ToggleRight size={28} className="text-emerald-400" /> : <ToggleLeft size={28} />}
                  </button>
                </div>
              </div>

              <div>
                <div className="mb-3 flex items-center gap-2">
                  <span className="text-[11px] font-bold uppercase tracking-wider text-slate-500">Ограничения сигнальной ноги</span>
                  <div className="h-px flex-1 bg-white/[.05]" />
                </div>
                <div className="mb-3 grid grid-cols-3 gap-3">
                  <Field label="Всего стратегий" hint="0 = ∞">
                    <input type="number" min={0} step={1} value={maxStrategies}
                      onChange={e => setMaxStrategies(Math.max(0, parseInt(e.target.value) || 0))} className={inputCls} />
                  </Field>
                  <Field label="Макс. лонг" hint="0 = ∞">
                    <input type="number" min={0} step={1} value={maxLongStrategies}
                      onChange={e => setMaxLongStrategies(Math.max(0, parseInt(e.target.value) || 0))} className={`${inputCls} border-emerald-900/40`} />
                  </Field>
                  <Field label="Макс. шорт" hint="0 = ∞">
                    <input type="number" min={0} step={1} value={maxShortStrategies}
                      onChange={e => setMaxShortStrategies(Math.max(0, parseInt(e.target.value) || 0))} className={`${inputCls} border-rose-900/40`} />
                  </Field>
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <Field label="Лимит маржи, USDT" hint="0 — без ограничений">
                    <input type="number" min={0} step={10} value={maxMarginUsdt}
                      onChange={e => setMaxMarginUsdt(Math.max(0, parseFloat(e.target.value) || 0))} className={inputCls} />
                  </Field>
                  <Field label="Повторов подряд" hint="0 = ∞">
                    <input type="number" min={0} step={1} value={maxSymConsecutiveRuns}
                      onChange={e => setMaxSymConsecutiveRuns(Math.max(0, parseInt(e.target.value) || 0))} className={inputCls} />
                  </Field>
                </div>
              </div>
            </div>
          </div>

          <div style={{ display: tab === 'signal' ? 'flex' : 'none' }} className="h-full flex-col overflow-hidden">
            <BotForm ref={signalRef} embedded initialKind="signal" bot={signalBot} onSubmit={() => {}} onClose={onClose} />
          </div>

          <div style={{ display: tab === 'hedge' ? 'flex' : 'none' }} className="h-full flex-col overflow-hidden">
            <HedgeBotForm ref={hedgeRef} embedded hideFilters bot={hedgeBot} onSubmit={() => {}} onClose={onClose} />
          </div>
        </div>

        {/* footer */}
        <div className="border-t border-white/[.06] px-5 py-3.5">
          {submitError && (
            <div className="mb-2.5 rounded-lg border border-rose-500/30 bg-rose-500/[.1] px-3 py-2 text-[12px] text-rose-300">{submitError}</div>
          )}
          <div className="flex items-center justify-end gap-2">
            <button
              type="button"
              onClick={onClose}
              disabled={submitting}
              className="inline-flex items-center gap-1.5 rounded-lg border border-white/[.08] bg-white/[.03] px-4 py-2 text-xs font-semibold text-slate-200 hover:bg-white/[.06] disabled:opacity-40"
            >
              Отмена
            </button>
            <button
              type="button"
              onClick={handleSave}
              disabled={!name.trim() || submitting}
              className="inline-flex items-center gap-1.5 rounded-lg bg-[linear-gradient(180deg,#d946ef,#a21caf)] px-4 py-2 text-xs font-semibold text-white shadow-[inset_0_1px_0_rgba(255,255,255,.18),0_4px_12px_-6px_rgba(217,70,239,.5)] disabled:cursor-not-allowed disabled:opacity-40"
            >
              {submitting ? 'Сохранение...' : (isEdit ? 'Сохранить' : 'Создать')}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
