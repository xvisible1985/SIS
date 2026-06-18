import { useState, useEffect, useCallback, useRef } from 'react';
import { apiClient } from '../../api/client';
import type { Bot, BotAction, Trigger, StrategyConfig } from './types';

type RawBot = Record<string, unknown>;

function parseBot(raw: RawBot): Bot {
  return {
    id:              raw.id              as string,
    name:            raw.name            as string,
    description:     raw.description     as string,
    fullDescription: (raw.fullDescription as string) || undefined,
    avatarUrl:       (raw.avatarUrl as string) || undefined,
    ownerId:         raw.ownerId         as string,
    ownerName:       raw.ownerName       as string,
    isOwn:           raw.isOwn           as boolean,
    isPublic:        raw.isPublic        as boolean,
    isOfficial:      raw.isOfficial      as boolean,
    status:          raw.status          as Bot['status'],
    sourceBotId:     raw.sourceBotId     as string | null,
    isFork:          raw.isFork          as boolean,
    symbolWhitelist: (raw.symbolWhitelist ?? []) as string[],
    symbolBlacklist: (raw.symbolBlacklist ?? []) as string[],
    triggers:        (raw.triggers       ?? []) as Trigger[],
    strategyConfig:  (raw.strategyConfig ?? {}) as StrategyConfig,
    deployCount:           raw.deployCount           as number,
    createdAt:             new Date(raw.createdAt as string),
    maxStrategies:         (raw.maxStrategies         as number) ?? 0,
    maxLongStrategies:     (raw.maxLongStrategies     as number) ?? 0,
    maxShortStrategies:    (raw.maxShortStrategies    as number) ?? 0,
    maxMarginUsdt:         (raw.maxMarginUsdt         as number) ?? 0,
    maxSymConsecutiveRuns: (raw.maxSymConsecutiveRuns as number) ?? 0,
    activeStrategiesCount: (raw.activeStrategiesCount as number) ?? 0,
    accountId:             (raw.accountId as string | null) ?? null,
    autoMode:              (raw.autoMode as boolean) ?? false,
    activeSecondsAcc:      (raw.activeSecondsAcc as number) ?? 0,
    activeSince:           (raw.activeSince as string) ?? null,
    approvalStatus:        (raw.approvalStatus as Bot['approvalStatus']) ?? null,
    price:                 (raw.price as number) ?? 0,
    spark:                 (raw.spark as number[] | null) ?? [],
    activeUsersCount:      (raw.activeUsersCount as number) ?? 0,
    tradesTotal:           (raw.tradesTotal as number) ?? 0,
    tradesWin:             (raw.tradesWin as number) ?? 0,
    netPnlTotal:           (raw.netPnlTotal as number) ?? 0,
    sourceAuthor:          (raw.sourceAuthor as string) || '',
  };
}

export function useBots() {
  const [catalog, setCatalog]       = useState<Bot[]>([]);
  const [mine, setMine]             = useState<Bot[]>([]);
  const [loading, setLoading]       = useState(true);
  const initializedRef              = useRef(false);

  const load = useCallback(async () => {
    if (!initializedRef.current) setLoading(true);
    try {
      const res = await apiClient.get<{ catalog: RawBot[]; mine: RawBot[] }>('/bots');
      setCatalog(res.data.catalog.map(parseBot));
      setMine(res.data.mine.map(parseBot));
    } finally {
      if (!initializedRef.current) {
        initializedRef.current = true;
        setLoading(false);
      }
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  // Refresh when any component signals a bot was updated externally (e.g. blacklist-add from StrategyCard).
  useEffect(() => {
    const handler = () => { load(); };
    window.addEventListener('bot-updated', handler);
    return () => window.removeEventListener('bot-updated', handler);
  }, [load]);

  const action = useCallback(async (a: BotAction) => {
    try {
      switch (a.type) {
        case 'start':   await apiClient.post(`/bots/${a.botId}/start`); break;
        case 'stop':    await apiClient.post(`/bots/${a.botId}/stop`); break;
        case 'deploy':  await apiClient.post(`/bots/${a.botId}/deploy`, {
          symbolWhitelist: a.symbolWhitelist,
          symbolBlacklist: a.symbolBlacklist,
        }); break;
        case 'fork':    await apiClient.post(`/bots/${a.botId}/fork`); break;
        case 'publish': await apiClient.post(`/bots/${a.botId}/publish`); break;
        case 'request-approval': await apiClient.post(`/bots/${a.botId}/request-approval`); break;
        case 'update':  await apiClient.patch(`/bots/${a.botId}`, a.data); break;
        case 'delete':  await apiClient.delete(`/bots/${a.botId}`); break;
        case 'create':  await apiClient.post('/bots', a.data); break;
      }
    } finally {
      await load();
    }
  }, [load]);

  return { catalog, mine, loading, action, refresh: load };
}
