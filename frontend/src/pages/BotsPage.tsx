import { useState, useMemo } from 'react';
import { BotsPage as BotsPageUI } from '../features/bots/BotsPage';
import { BotForm } from '../features/bots/components/BotForm';
import { HedgeBotForm } from '../features/bots/components/HedgeBotForm';
import { MatrixBotForm } from '../features/bots/components/MatrixBotForm';
import { MultiBotForm } from '../features/bots/components/MultiBotForm';
import { BotTypePickerModal } from '../features/bots/components/BotTypePickerModal';
import { useBots } from '../features/bots/api';
import { useBotSignalCounts } from '../hooks/useBotSignalCounts';
import type { BotSignalCount } from '../hooks/useBotSignalCounts';
import type { Bot, BotKind, CreateBotInput } from '../features/bots/types';
import type { MyBot, FeaturedBot, BotStrategy, RiskLevel, TradeMode } from '../features/bots/ui-types';

// pairedBot is the Мультибот's hedge leg (see migration 092) — its own trade stats get
// folded into the one visible card since the hedge row itself is never shown separately.
function toMyBot(b: Bot, sc: BotSignalCount | undefined, pairedBot?: Bot): MyBot {
  const wl = b.symbolWhitelist ?? [];
  const symbolsTotal = sc
    ? String(sc.totalCount)
    : wl.length === 0 ? 'все' : String(wl.length);

  return {
    id:               b.id,
    tplId:            b.sourceBotId,
    name:             b.name,
    description:      b.description ?? '',
    avatarUrl:        b.avatarUrl,
    strategy: (b.strategyConfig.direction === 'long'
      ? 'signal'
      : b.strategyConfig.direction === 'short'
        ? 'signal'
        : 'grid') as BotStrategy,
    status:           b.status === 'active' ? 'running' : b.status === 'stopped' ? 'stopped' : 'paused',
    pair:             b.strategyConfig.symbol ?? 'BTC/USDT',
    exchange:         'bybit',
    capital:          b.strategyConfig.grid_size_usdt ?? 0,
    started:          new Date(b.createdAt),
    botKind:           b.pairedBotId ? 'multi' : b.strategyConfig.bot_kind,
    lev:              1,
    mode:             'futures' as TradeMode,
    symbolsTotal,
    symbolsWithSignal: sc ? String(sc.signalCount) : '—',
    symbolsLimit:      '—',
    custom:            !b.sourceBotId,
    isOfficial:        b.isOfficial,
    approvalStatus:    b.approvalStatus,
    activeSecondsAcc:  b.activeSecondsAcc,
    activeSince:       b.activeSince,
    tradesTotal:       (b.tradesTotal ?? 0) + (pairedBot?.tradesTotal ?? 0),
    tradesWin:         (b.tradesWin ?? 0) + (pairedBot?.tradesWin ?? 0),
    netPnlTotal:       (b.netPnlTotal ?? 0) + (pairedBot?.netPnlTotal ?? 0),
    // шаблонный → автор шаблона; кастомный → ник владельца
    sourceAuthor:      b.sourceBotId ? (b.sourceAuthor || b.ownerName) : b.ownerName,
    config:            b.strategyConfig as Record<string, unknown>,
  };
}

function toFeaturedBot(b: Bot): FeaturedBot {
  return {
    id:        b.id,
    name:      b.name,
    author:    b.isOfficial ? 'NovaBot' : b.ownerName,
    avatarUrl: b.avatarUrl,
    botKind:   (b.strategyConfig?.bot_kind as BotKind) ?? 'signal',
    strategy:  (b.strategyConfig?.strategy_type as BotStrategy) ?? 'grid',
    risk:      'medium' as RiskLevel,
    verified:  b.isOfficial,
    fire:      b.deployCount > 10,
    price:     b.price ?? 0,
    fullDescription: b.fullDescription || undefined,
    desc:      b.description,
    pairs:     b.symbolWhitelist.length > 0 ? b.symbolWhitelist : ['BTC'],
    minCap:    0,
    fee:       0,
    users:     b.activeUsersCount ?? 0,
    rating:    0,
    perfMonth: 0,
    perfTotal: 0,
    winRate:   0,
    sharpe:    0,
    drawdown:  0,
    spark:     (b.spark && b.spark.length >= 2) ? b.spark : [0, 0],
    exchanges: ['bybit'],
    lev:       1,
    mode:      'futures' as TradeMode,
    tags:      b.symbolWhitelist,
  };
}

export function BotsPage() {
  const { catalog, mine, loading, action, refresh } = useBots();
  const signalCounts = useBotSignalCounts(!loading);

  const [formMode, setFormMode] = useState<'create' | 'edit' | null>(null);
  const [editBot, setEditBot] = useState<Bot | null>(null);
  const [editHedgeBot, setEditHedgeBot] = useState<Bot | null>(null);

  // A Мультибот's hedge leg is an ordinary bots row (bot_kind='hedge') that only exists to
  // be driven by its paired signal leg's whitelist — it must never render as its own card.
  const visibleMine = useMemo(
    () => mine.filter(b => !(b.strategyConfig?.bot_kind === 'hedge' && b.pairedBotId)),
    [mine],
  );

  const takenSymbols = useMemo((): Map<string, string> => {
    const map = new Map<string, string>()
    for (const b of mine) {
      if (b.id === editBot?.id) continue  // exclude the bot being edited
      if (b.status !== 'active') continue
      for (const sym of b.symbolWhitelist) {
        if (!sym.includes('*') && !map.has(sym)) {
          map.set(sym, b.name)
        }
      }
    }
    return map
  }, [mine, editBot?.id])
  const [kindPickerOpen, setKindPickerOpen] = useState(false);
  const [selectedKind, setSelectedKind] = useState<BotKind>('signal');

  if (loading) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-slate-500">
        Загрузка...
      </div>
    );
  }

  const handleCreate = () => {
    setEditBot(null);
    setEditHedgeBot(null);
    setKindPickerOpen(true);
  };

  const handleKindSelect = (kind: BotKind) => {
    setSelectedKind(kind);
    setKindPickerOpen(false);
    setFormMode('create');
  };

  const handleEditBot = (id: string) => {
    const bot = mine.find((b) => b.id === id) ?? null;
    setEditBot(bot);
    if (bot?.pairedBotId) {
      setEditHedgeBot(mine.find((b) => b.id === bot.pairedBotId) ?? null);
      setSelectedKind('multi');
    } else {
      setEditHedgeBot(null);
      setSelectedKind((bot?.strategyConfig?.bot_kind as BotKind) ?? 'signal');
    }
    setFormMode('edit');
  };

  const handleFormClose = () => {
    setFormMode(null);
    setEditBot(null);
    setEditHedgeBot(null);
    setSelectedKind('signal');
  };

  const handleFormSubmit = async (data: CreateBotInput): Promise<{ warnings?: string[] } | void> => {
    if (formMode === 'create') {
      await action({ type: 'create', data });
      return;
    } else if (formMode === 'edit' && editBot) {
      return action({ type: 'update', botId: editBot.id, data }) as Promise<{ warnings?: string[] } | void>;
    }
  };

  return (
    <>
      <BotsPageUI
        myBots={visibleMine.map(b => toMyBot(
          b,
          signalCounts.get(b.id),
          b.pairedBotId ? mine.find((p) => p.id === b.pairedBotId) : undefined,
        ))}
        featured={catalog.map(toFeaturedBot)}
        onCreateBot={handleCreate}
        onExportBots={() => {}}
        onToggleBot={(id, next) =>
          action({
            type: next === 'running' ? 'start' : 'stop',
            botId: id,
          })
        }
        onEditBot={handleEditBot}
        onDeleteBot={(id) => action({ type: 'delete', botId: id })}
        onRequestApproval={(id) => action({ type: 'request-approval', botId: id })}
        onCloneTpl={(tplId) => action({ type: 'deploy', botId: tplId })}
      />

      {kindPickerOpen && (
        <BotTypePickerModal
          onSelect={handleKindSelect}
          onClose={() => setKindPickerOpen(false)}
        />
      )}

      {formMode !== null && selectedKind === 'multi' && (
        <MultiBotForm
          signalBot={editBot ?? undefined}
          hedgeBot={editHedgeBot ?? undefined}
          onClose={handleFormClose}
          onSaved={refresh}
        />
      )}

      {formMode !== null && selectedKind === 'hedge' && (
        <HedgeBotForm
          bot={editBot ?? undefined}
          takenSymbols={takenSymbols}
          onSubmit={handleFormSubmit}
          onClose={handleFormClose}
        />
      )}

      {formMode !== null && selectedKind === 'matrix' && (
        <MatrixBotForm
          bot={editBot ?? undefined}
          takenSymbols={takenSymbols}
          onSubmit={handleFormSubmit}
          onClose={handleFormClose}
        />
      )}

      {formMode !== null && selectedKind !== 'hedge' && selectedKind !== 'matrix' && selectedKind !== 'multi' && (
        <BotForm
          bot={editBot ?? undefined}
          initialKind={selectedKind}
          onSubmit={handleFormSubmit}
          onClose={handleFormClose}
        />
      )}

    </>
  );
}
