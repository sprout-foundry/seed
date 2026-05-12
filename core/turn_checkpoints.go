// Package core provides a conversation engine for LLM-powered agents.
//
// Checkpoint functionality is split across three files:
//   - turn_summary.go: TurnCheckpoint and TurnSummaryBuilder
//   - checkpoint_compaction.go: checkpoint compaction and recording
//   - checkpoint_shifting.go: checkpoint index shifting after compaction

package core
