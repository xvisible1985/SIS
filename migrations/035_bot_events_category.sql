ALTER TABLE bot_events ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT 'system';

UPDATE bot_events SET category = 'tick'     WHERE message LIKE 'Тик:%';
UPDATE bot_events SET category = 'strategy' WHERE message LIKE '%стратегия%'
                                             OR message LIKE '%Стратегия%'
                                             OR message LIKE '%Реактивный%';
UPDATE bot_events SET category = 'trade'    WHERE message LIKE '%TP%'
                                             OR message LIKE '%SL%'
                                             OR message LIKE '%сделка%';
UPDATE bot_events SET category = 'user'     WHERE message LIKE '%Пользователь%'
                                             OR message LIKE '%настройки%';
