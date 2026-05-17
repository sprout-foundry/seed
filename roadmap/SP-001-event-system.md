# SP-001: Event System

**Status:** ✅ Complete — see [docs/architecture.md](../docs/architecture.md)

EventBus provides thread-safe pub/sub. Agent publishes events at lifecycle points: `query_started`, `tool_start`, `tool_end`, `error`, `metrics_update`, `stream_chunk`, `agent_message`, `compaction`, `query_completed`. OutputManager routes chunked output to event publisher.
