import { useState, useEffect, useCallback } from 'react';
import { apiClient } from '../../api/client';
import type { Bot, CreateBotInput, StrategyConfig, Trigger } from '../bots/types';

type RawBot = Record<string, unknown>;

function parseBot(raw: RawBot): Bot {
  return {
    id:              raw.id              as string,
    name:            raw.name            as string,
    description:     raw.description     as string,
    fullDescription: (raw.fullDescription as string) || undefined,
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
  };
}

export function useAdminBots() {
  const [bots, setBots]     = useState<Bot[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await apiClient.get<RawBot[]>('/admin/bots');
      setBots(res.data.map(parseBot));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  const create = useCallback(async (data: CreateBotInput) => {
    await apiClient.post('/admin/bots', data);
    await load();
  }, [load]);

  const remove = useCallback(async (botId: string) => {
    await apiClient.delete(`/bots/${botId}`);
    await load();
  }, [load]);

  const togglePublic = useCallback(async (botId: string, isPublic: boolean) => {
    await apiClient.patch(`/bots/${botId}`, { isPublic });
    await load();
  }, [load]);

  const update = useCallback(async (botId: string, data: Partial<CreateBotInput>) => {
    await apiClient.patch(`/bots/${botId}`, data);
    await load();
  }, [load]);

  return { bots, loading, create, remove, togglePublic, update, refresh: load };
}
