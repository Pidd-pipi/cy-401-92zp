package constants

// Contract statuses.
const (
	ContractPendingSignature = "pending_signature"
	ContractInProgress       = "in_progress"
	ContractPendingReview    = "pending_review"
	ContractCompleted        = "completed"
	ContractTerminated       = "terminated"
)

// Contract stage statuses. A stage is one payment milestone whose funds
// are only released after Party A confirms it; the next stage cannot
// start until the previous one is confirmed.
const (
	StagePending    = "pending"     // not started, waiting for the previous stage to be confirmed
	StageInProgress = "in_progress" // current stage, Party B is delivering
	StageSubmitted  = "submitted"   // Party B submitted, waiting for Party A to confirm payment
	StageRejected   = "rejected"    // Party A rejected the delivery, Party B is reworking
	StageDone       = "done"        // Party A confirmed payment
)

// ValidContractStatus reports whether a status is valid.
func ValidContractStatus(s string) bool {
	switch s {
	case ContractPendingSignature, ContractInProgress, ContractPendingReview, ContractCompleted, ContractTerminated:
		return true
	}
	return false
}

// ValidStageStatus reports whether a stage status is valid.
func ValidStageStatus(s string) bool {
	switch s {
	case StagePending, StageInProgress, StageSubmitted, StageRejected, StageDone:
		return true
	}
	return false
}
