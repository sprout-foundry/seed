# SP-004: Output Routing

**Status:** ✅ Complete — see [docs/architecture.md](../docs/architecture.md)

OutputManager provides streaming buffers (content/reasoning), async output channel, event metadata, and flush callback. PublishOutput routes output to event publisher as stream_chunk/agent_message events. Wired into Agent on construction.
