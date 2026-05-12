package core

// TurnCheckpoint captures a summary of a completed conversation turn.
// It records the message range consumed by the turn and a compact summary
// that can replace the original messages during context compaction.
type TurnCheckpoint struct {
	// StartIndex is the index of the first message in the turn (the user query).
	StartIndex int `json:"start_index"`

	// EndIndex is the index of the last message in the turn (the final assistant response).
	EndIndex int `json:"end_index"`

	// Summary is a concise description of what happened in the turn.
	Summary string `json:"summary"`

	// ActionableSummary is a bullet-list of accomplishments with file paths,
	// commands run, and other concrete details useful for continued context.
	ActionableSummary string `json:"actionable_summary"`
}
