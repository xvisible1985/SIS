import { useState, useEffect, useCallback } from 'react';
import { apiClient } from '../../api/client';
import type { Bot, BotAction, Trigger, StrategyConfig } from './types';

type RawBot = Record<string, unknown>;

function parseBot(raw: RawBot): Bot {
  return {
    id:              raw.id              as string,
    name:            raw.name            as string,
    description:     raw.description     as string,
    ownerId:         raw.ownerId         as string,
    ownerName:       raw.ownerName       as string,
    isOwn:           raw.isOwn           as boolean,
    isPublic:        raw.isPublic        as boolean,
    status:          raw.status          as Bot['status'],
    sourceBotId:     raw.sourceBotId     as string | null,
    isFork:          raw.isFork          as boolean,
    symbolWhitelist: (raw.symbolWhitelist ?? []) as string[],
    symbolBlacklist: (raw.symbolBlacklist ?? []) as string[],
    triggers:        (raw.triggers       ?? []) as Trigger[],
    strategyConfig:  (raw.strategyConfig ?? {}) as StrategyConfig,
    deployCount:     raw.deployCount     as number,
    createdAt:       new Date(raw.createdAt as string),
  };
}

export function useBots() {
  const [catalog, setCatalog] = useState<Bot[]>([]);
  const [mine, setMine]       = useState<Bot[]>([]);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await apiClient.get<{ catalog: RawBot[]; mine: RawBot[] }>('/bots');
      setCatalog(res.data.catalog.map(parseBot));
      setMine(res.data.mine.map(parseBot));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  const action = useCallback(async (a: BotAction) => {
    switch (a.type) {
      case 'start':   await apiClient.post(`/bots/${a.botId}/start`); break;
      case 'stop':    await apiClient.post(`/bots/${a.botId}/stop`); break;
      case 'deploy':  await apiClient.post(`/bots/${a.botId}/deploy`, {
        symbolWhitelist: a.symbolWhitelist,
        symbolBlacklist: a.symbolBlacklist,
      }); break;
      case 'fork':    await apiClient.post(`/bots/${a.botId}/fork`); break;
      case 'publish': await apiClient.post(`/bots/${a.botId}/publish`); break;
      case 'update':  await apiClient.patch(`/bots/${a.botId}`, a.data); break;
      case 'delete':  await apiClient.delete(`/bots/${a.botId}`); break;
      case 'create':  await apiClient.post('/bots', a.data); break;
    }
    await load();
  }, [load]);

  return { catalog, mine, loading, action, refresh: load };
}
