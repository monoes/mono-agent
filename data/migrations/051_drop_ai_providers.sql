-- The in-app AI provider stack is gone: all AI goes through local agents
-- via monomind (docs/plans/local-agent-monomind-delegation.md §6). This
-- table held only provider settings; the API keys lived in the vault as
-- kind 'ai_provider' entries, which secrets.RetireAIProviderEntries backs
-- up to a file and then deletes at startup, after this migration runs.
-- IF EXISTS: a database that never opened the provider store has no table.
DROP TABLE IF EXISTS ai_providers;
