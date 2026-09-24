package constants

// Contract statuses.
const (
	ContractPendingSignature = "pending_signature"
	ContractInProgress       = "in_progress"
	ContractPendingReview    = "pending_review"
	ContractCompleted        = "completed"
	ContractTerminated       = "terminated"
)

// Contract stage statuses.
const (
	StagePending    = "pending"     // 未开始
	StageInProgress = "in_progress" // 进行中（乙方尚未提交）
	StageSubmitted  = "submitted"   // 乙方已提交，待甲方确认付款
	StageRejected   = "rejected"    // 甲方驳回，乙方整改后重新提交
	StagePaid       = "paid"        // 甲方已确认付款
)

// ValidContractStatus reports whether a status is valid.
func ValidContractStatus(s string) bool {
	switch s {
	case ContractPendingSignature, ContractInProgress, ContractPendingReview, ContractCompleted, ContractTerminated:
		return true
	}
	return false
}
