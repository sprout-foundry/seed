# SP-014: Compaction Hardening

**Status:** ✅ Complete — see [docs/compaction.md](../docs/compaction.md)

4-phase compaction algorithm: drop oldest checkpoint summaries, drop oldest complete turns, emergency truncation (head+tail), force-drop. Checkpoint summaries use ActionableSummary with 500-char guard. Meta["checkpoint"]="true" marker on summary messages. checkpoint_shifting.go removed (dead code). Recent 24 messages always preserved in full.
