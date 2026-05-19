import { useState } from 'react';
import { Eye, EyeOff, Copy, Check } from 'lucide-react';
import { maskKey } from '../utils';

type Props = { value: string };

export function MaskedKey({ value }: Props) {
  const [show, setShow] = useState(false);
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // user gesture / permission issues — silently ignore
    }
  };

  return (
    <div className="inline-flex items-center gap-1.5 font-mono text-[11px] text-slate-300">
      <span className="select-all">{show ? value : maskKey(value)}</span>
      <button
        type="button"
        onClick={() => setShow((v) => !v)}
        title={show ? 'Скрыть' : 'Показать'}
        className="flex h-[22px] w-[22px] items-center justify-center rounded border border-white/[.08] bg-white/[.04] text-slate-400 hover:text-slate-200"
      >
        {show ? <EyeOff size={11} /> : <Eye size={11} />}
      </button>
      <button
        type="button"
        onClick={copy}
        title={copied ? 'Скопировано' : 'Скопировать'}
        className="flex h-[22px] w-[22px] items-center justify-center rounded border border-white/[.08] bg-white/[.04] text-slate-400 hover:text-slate-200"
      >
        {copied ? <Check size={11} className="text-emerald-300" /> : <Copy size={11} />}
      </button>
    </div>
  );
}
