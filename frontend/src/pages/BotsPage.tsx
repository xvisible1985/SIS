import { useState } from 'react';
import { BotsPage as BotsPageUI } from '../features/bots/BotsPage';
import { BotForm } from '../features/bots/components/BotForm';
import { HedgeBotForm } from '../features/bots/components/HedgeBotForm';
import { BotTypePickerModal } from '../features/bots/components/BotTypePickerModal';
import { DeployModal } from '../features/bots/components/DeployModal';
import { useBots } from '../features/bots/api';
import { useBotSignalCounts } from '../hooks/useBotSignalCounts';
import type { BotSignalCount } from '../hooks/useBotSignalCounts';
import type { Bot, BotKind, CreateBotInput } from '../features/bots/types';
import type { MyBot, FeaturedBot, BotStrategy, RiskLevel, TradeMode } from '../features/bots/ui-types';

function toMyBot(b: Bot, sc: BotSignalCount | undefined): MyBot {
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
    botKind:           b.strategyConfig.bot_kind,
    lev:              1,
    mode:             'futures' as TradeMode,
    symbolsTotal,
    symbolsWithSignal: sc ? String(sc.signalCount) : '—',
    symbolsLimit:      '—',
    custom:            !b.sourceBotId,
    approvalStatus:    b.approvalStatus,
    activeSecondsAcc:  b.activeSecondsAcc,
    activeSince:       b.activeSince,
    config:            b.strategyConfig as Record<string, unknown>,
  };
}

function toFeaturedBot(b: Bot): FeaturedBot {
  return {
    id:        b.id,
    name:      b.name,
    author:    b.ownerName,
    strategy:  'grid' as BotStrategy,
    risk:      'medium' as RiskLevel,
    verified:  b.isOfficial,
    fire:      b.deployCount > 10,
    price:     0,
    desc:      b.description,
    pairs:     b.symbolWhitelist.length > 0 ? b.symbolWhitelist : ['BTC'],
    minCap:    0,
    fee:       0,
    users:     b.deployCount,
    rating:    4.0,
    perfMonth: 0,
    perfTotal: 0,
    winRate:   0,
    sharpe:    0,
    drawdown:  0,
    spark:     [0, 0],
    exchanges: ['bybit'],
    lev:       1,
    mode:      'futures' as TradeMode,
    tags:      b.symbolWhitelist,
  };
}

export function BotsPage() {
  const { catalog, mine, loading, action } = useBots();
  const signalCounts = useBotSignalCounts(!loading);

  const [formMode, setFormMode] = useState<'create' | 'edit' | null>(null);
  const [editBot, setEditBot] = useState<Bot | null>(null);
  const [deployBot, setDeployBot] = useState<Bot | null>(null);
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
    setSelectedKind((bot?.strategyConfig?.bot_kind as BotKind) ?? 'signal');
    setFormMode('edit');
  };

  const handleFormClose = () => {
    setFormMode(null);
    setEditBot(null);
    setSelectedKind('signal');
  };

  const handleFormSubmit = async (data: CreateBotInput) => {
    if (formMode === 'create') {
      await action({ type: 'create', data });
    } else if (formMode === 'edit' && editBot) {
      await action({ type: 'update', botId: editBot.id, data });
    }
  };

  const handleLaunchTpl = (tplId: string) => {
    const bot = catalog.find((b) => b.id === tplId) ?? null;
    if (bot) {
      setDeployBot(bot);
    } else {
      action({ type: 'deploy', botId: tplId });
    }
  };

  const handleDeploy = (whitelist: string[], blacklist: string[]) => {
    if (!deployBot) return;
    action({
      type: 'deploy',
      botId: deployBot.id,
      symbolWhitelist: whitelist.length > 0 ? whitelist : undefined,
      symbolBlacklist: blacklist.length > 0 ? blacklist : undefined,
    });
  };

  return (
    <>
      <BotsPageUI
        myBots={mine.map(b => toMyBot(b, signalCounts.get(b.id)))}
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
        onLaunchTpl={handleLaunchTpl}
        onConfigureTpl={handleLaunchTpl}
        onCloneTpl={(tplId) => action({ type: 'fork', botId: tplId })}
      />

      {kindPickerOpen && (
        <BotTypePickerModal
          onSelect={handleKindSelect}
          onClose={() => setKindPickerOpen(false)}
        />
      )}

      {formMode !== null && selectedKind === 'hedge' && (
        <HedgeBotForm
          bot={editBot ?? undefined}
          onSubmit={handleFormSubmit}
          onClose={handleFormClose}
        />
      )}

      {formMode !== null && selectedKind !== 'hedge' && (
        <BotForm
          bot={editBot ?? undefined}
          initialKind={selectedKind}
          onSubmit={handleFormSubmit}
          onClose={handleFormClose}
        />
      )}

      {deployBot && (
        <DeployModal
          bot={deployBot}
          onDeploy={handleDeploy}
          onClose={() => setDeployBot(null)}
        />
      )}
    </>
  );
}
