package dto

// GenerateContractRequest is the payload for creating a contract from a bid.
type GenerateContractRequest struct {
	BidID       uint   `json:"bidId" validate:"required"`
	PaymentType string `json:"paymentType" validate:"required,oneof=installments one_time"`
}

// SubmitStageRequest is the payload for Party B submitting a stage delivery.
type SubmitStageRequest struct {
	StageIndex int    `json:"stageIndex" validate:"min=0"`
	Note       string `json:"note" validate:"max=500"`
}

// ConfirmStageRequest is the payload for Party A confirming a stage payment.
type ConfirmStageRequest struct {
	StageIndex int `json:"stageIndex" validate:"min=0"`
}

// RejectStageRequest is the payload for Party A rejecting a stage delivery.
type RejectStageRequest struct {
	StageIndex int    `json:"stageIndex" validate:"min=0"`
	Reason     string `json:"reason" validate:"required,max=500"`
}
