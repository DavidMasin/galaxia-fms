package field

// MatchTimeSnapshot returns the current match state and clock, in the same shape as the "matchTime" message.
func (arena *Arena) MatchTimeSnapshot() MatchTimeMessage {
	return arena.generateMatchTimeMessage().(MatchTimeMessage)
}

// RealtimeScoreSnapshot returns the live score, in the same shape as the "realtimeScore" message.
func (arena *Arena) RealtimeScoreSnapshot() *RealtimeScoreMessage {
	return arena.generateRealtimeScoreMessage().(*RealtimeScoreMessage)
}
