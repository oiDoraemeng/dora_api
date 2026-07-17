UPDATE accounts
SET extra = jsonb_set(
    COALESCE(extra, '{}'::jsonb),
    '{openai_long_context_billing_enabled}',
    'true'::jsonb,
    true
)
WHERE platform = 'openai'
  AND (
    NOT (COALESCE(extra, '{}'::jsonb) ? 'openai_long_context_billing_enabled')
    OR extra->'openai_long_context_billing_enabled' = 'false'::jsonb
  );
