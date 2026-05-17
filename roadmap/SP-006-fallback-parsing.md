# SP-006: Fallback Parsing

**Status:** ✅ Complete — see [docs/conversation-flow.md](../docs/conversation-flow.md)

FallbackParser extracts tool calls from malformed LLM content using 7 strategies: JSON fences, bare JSON, XML blocks, tool blocks, tool_use blocks, function-name patterns, named tool blocks. Normalizes IDs, types, and JSON arguments. Wired into conversation loop when structured tool_calls is empty but content has patterns.
