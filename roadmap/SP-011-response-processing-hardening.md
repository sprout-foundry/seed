# SP-011: Response Processing Hardening

**Status:** ✅ Complete — see [docs/conversation-flow.md](../docs/conversation-flow.md)

ToolCallNormalizer strips channel suffixes, generates missing IDs, deduplicates by ID+args, repairs broken JSON arguments, and forces Type="function". Malformed calls trigger transient message for model re-emit. Finish reason dispatch handles stop (validate/incomplete/tentative), length (continue), content_filter (retry once, then error), tool_calls, and empty/default. Blank/repetitive iteration detection with consecutiveBlank counter (threshold 2). ANSI sanitization strips escape codes from content.
