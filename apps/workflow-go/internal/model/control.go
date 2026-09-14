package model

// ExecutionControl is the authoritative parent intent, not a terminal outcome.
type ExecutionControl struct {
	State    string `json:"controlState"`
	Revision int64  `json:"controlRevision"`
	Phase    string `json:"phase"`
}

type ExecutionControlRef struct {
	TenantID    string `json:"tenantId"`
	ExecutionID string `json:"executionId"`
}

func TerminalExecutionPhase(phase string) bool {
	return phase == "completed" || phase == "failed" || phase == "cancelled"
}
