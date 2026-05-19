import { BotsPage as BotsPageUI } from '../features/bots/BotsPage';
import { useBots } from '../features/bots/api';
import type { Bot } from '../features/bots/types';
import type { MyBot, FeaturedBot, BotStrategy, RiskLevel, TradeMode } from '../features/bots/ui-types';

function toMyBot(b: Bot): MyBot {
  return {
    id:       b.id,
    tplId:    b.sourceBotId,
    name:     b.name,
    strategy: (b.strategyConfig.direction === 'long'
      ? 'trend'
      : b.strategyConfig.direction === 'short'
        ? 'trend'
        : 'grid') as BotStrategy,
    status:   b.status === 'active' ? 'running' : b.status === 'stopped' ? 'stopped' : 'paused',
    pair:     b.strategyConfig.symbol ?? 'BTC/USDT',
    exchange: 'bybit',
    capital:  b.strategyConfig.grid_size_usdt ?? 0,
    balance:  b.strategyConfig.grid_size_usdt ?? 0,
    pnlMonth: 0,
    pnlTotal: 0,
    trades:   0,
    winRate:  0,
    started:  new Date(b.createdAt),
    lev:      1,
    mode:     'futures' as TradeMode,
    custom:   !b.sourceBotId,
    config:   b.strategyConfig as Record<string, unknown>,
  };
}

function toFeaturedBot(b: Bot): FeaturedBot {
  return {
    id:        b.id,
    name:      b.name,
    author:    b.ownerName,
    strategy:  'grid' as BotStrategy,
    risk:      'medium' as RiskLevel,
    verified:  false,
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

  if (loading) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-slate-500">
        Загрузка...
      </div>
    );
  }

  return (
    <BotsPageUI
      myBots={mine.map(toMyBot)}
      featured={catalog.map(toFeaturedBot)}
      onCreateBot={() => {}}
      onExportBots={() => {}}
      onToggleBot={(id, next) =>
        action({
          type: next === 'running' ? 'start' : 'stop',
          botId: id,
        })
      }
      onEditBot={() => {}}
      onMoreBot={() => {}}
      onLaunchTpl={(tplId) => action({ type: 'deploy', botId: tplId })}
      onConfigureTpl={() => {}}
      onCloneTpl={(tplId) => action({ type: 'fork', botId: tplId })}
    />
  );
}
