-- OpenCode Zen exposes the OpenAI Chat Completions-compatible endpoint. Keep
-- the persisted account capability aligned with the request-time invariant so
-- existing direct, proxy, cooling, and disabled workers are repaired before
-- their next reconciliation.
UPDATE accounts
SET extra = jsonb_set(
        COALESCE(extra, '{}'::jsonb),
        '{openai_responses_mode}',
        '"force_chat_completions"'::jsonb,
        true
    ),
    updated_at = NOW()
WHERE platform = 'openai'
  AND type = 'apikey'
  AND deleted_at IS NULL
  AND LOWER(BTRIM(COALESCE(extra->>'provider_mode', ''))) = 'opencode_zen'
  AND extra->>'openai_responses_mode' IS DISTINCT FROM 'force_chat_completions';
