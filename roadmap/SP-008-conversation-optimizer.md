# SP-008: Conversation Optimizer

**Status:** ✅ Complete — see [docs/compaction.md](../docs/compaction.md)

ConversationOptimizer deduplicates file reads (keeps latest per path, max 5 records), shell commands (transient commands only, max 10 records), and identical tool results (SHA-256 hash). Runs before compaction in prepareMessages(). Opt-in via Options.Optimizer.
