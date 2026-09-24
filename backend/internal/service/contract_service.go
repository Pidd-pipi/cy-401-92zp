package service

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/gigmatch/gigmatch/internal/constants"
	"github.com/gigmatch/gigmatch/internal/dto"
	"github.com/gigmatch/gigmatch/internal/model"
	"github.com/gigmatch/gigmatch/internal/repository"
)

// ContractService manages contracts.
type ContractService struct {
	contracts    *repository.ContractRepository
	requirements *repository.RequirementRepository
	logs         *OperationLogService
	logger       *slog.Logger
}

// NewContractService builds a ContractService.
func NewContractService(contracts *repository.ContractRepository, requirements *repository.RequirementRepository, logs *OperationLogService, logger *slog.Logger) *ContractService {
	return &ContractService{contracts: contracts, requirements: requirements, logs: logs, logger: logger}
}

// ListByParty returns contracts involving the caller.
func (s *ContractService) ListByParty(userID uint) ([]model.Contract, error) {
	list, err := s.contracts.ListByParty(userID)
	if err != nil {
		return nil, fmt.Errorf("list contracts: %w", err)
	}
	return list, nil
}

// Get loads a contract.
func (s *ContractService) Get(id uint) (*model.Contract, error) {
	return s.contracts.FindByID(id)
}

// CreateFromBid builds a contract from an accepted bid.
func (s *ContractService) CreateFromBid(r *model.Requirement, bid *model.Bid, requesterID uint, requesterName string, paymentType string) (*model.Contract, error) {
	if paymentType == "" {
		paymentType = "one_time"
	}
	// Every stage starts unpaid. Payment is released one stage at a time:
	// Party B delivers, Party A confirms payment, then the next stage opens.
	stages := []model.ContractStage{
		{Name: "项目启动", Amount: bid.Amount * 0.3, Status: constants.StagePending, DueAt: "签约后3日内"},
		{Name: "中期交付", Amount: bid.Amount * 0.4, Status: constants.StagePending, DueAt: "工期过半"},
		{Name: "验收结项", Amount: bid.Amount * 0.3, Status: constants.StagePending, DueAt: "验收通过后"},
	}
	contract := &model.Contract{
		ContractNo:    fmt.Sprintf("CY-%d-%d", r.ID, bid.ID),
		TotalAmount:   bid.Amount,
		PaymentType:   paymentType,
		Stages:        stages,
		Status:        constants.ContractPendingSignature,
		RequirementID: r.ID,
		PartyAID:      requesterID,
		PartyBID:      bid.BidderID,
	}
	if err := s.contracts.Create(contract); err != nil {
		return nil, fmt.Errorf("create contract: %w", err)
	}
	s.logs.Record(requesterID, requesterName, "contract.create", "contract", contract.ID, fmt.Sprintf("生成合同 %s", contract.ContractNo))
	return contract, nil
}

// Sign confirms a contract by either party. Once signed the first stage
// becomes active and Party B can submit it.
func (s *ContractService) Sign(id uint, userID uint, userName string) (*model.Contract, error) {
	c, err := s.contracts.FindByID(id)
	if err != nil {
		return nil, err
	}
	if c.PartyAID != userID && c.PartyBID != userID {
		return nil, constants.ErrForbidden
	}
	if c.Status != constants.ContractPendingSignature {
		return nil, constants.NewAppError(constants.CodeConflict, "合同当前不可签署")
	}
	c.Status = constants.ContractInProgress
	if len(c.Stages) > 0 {
		c.Stages[0].Status = constants.StageInProgress
	}
	if err := s.contracts.Update(c); err != nil {
		return nil, fmt.Errorf("sign contract: %w", err)
	}
	s.logs.Record(userID, userName, "contract.sign", "contract", c.ID, "签署确认合同")
	return c, nil
}

// SubmitStage records Party B's delivery of a stage. The stage must be the
// currently active one (in_progress, or rejected when reworking). Submitting
// an already-submitted/done stage is a no-op so a repeated click counts once.
func (s *ContractService) SubmitStage(id uint, req dto.SubmitStageRequest, userID uint, userName string) (*model.Contract, error) {
	c, err := s.contracts.FindByID(id)
	if err != nil {
		return nil, err
	}
	if c.PartyBID != userID {
		return nil, constants.ErrForbidden
	}
	if c.Status != constants.ContractInProgress && c.Status != constants.ContractPendingReview {
		return nil, constants.NewAppError(constants.CodeConflict, "合同当前不可提交阶段")
	}
	stage, err := s.stageAt(c, req.StageIndex)
	if err != nil {
		return nil, err
	}

	switch stage.Status {
	case constants.StageSubmitted:
		// Duplicate submission: count once.
		return c, nil
	case constants.StageDone:
		return nil, constants.NewAppError(constants.CodeConflict, "该阶段甲方已确认付款，不能重复提交")
	case constants.StagePending:
		return nil, constants.NewAppError(constants.CodeConflict, "上一阶段尚未确认付款，不能提前提交")
	}

	now := time.Now()
	stage.Status = constants.StageSubmitted
	stage.Note = req.Note
	stage.RejectReason = ""
	stage.RejectedAt = nil
	stage.SubmittedAt = &now
	c.Status = constants.ContractPendingReview
	if err := s.contracts.Update(c); err != nil {
		return nil, fmt.Errorf("submit stage: %w", err)
	}
	s.logs.Record(userID, userName, "contract.stage_submit", "contract", c.ID, fmt.Sprintf("提交第%d阶段「%s」", req.StageIndex+1, stage.Name))
	return c, nil
}

// ConfirmStage releases payment for a submitted stage (Party A only). The
// next stage opens immediately; confirming the final stage completes the
// contract. Confirming a non-submitted stage is a no-op when it is already
// done, so a repeated click counts once.
func (s *ContractService) ConfirmStage(id uint, req dto.ConfirmStageRequest, userID uint, userName string) (*model.Contract, error) {
	c, err := s.contracts.FindByID(id)
	if err != nil {
		return nil, err
	}
	if c.PartyAID != userID {
		return nil, constants.ErrForbidden
	}
	if c.Status != constants.ContractInProgress && c.Status != constants.ContractPendingReview {
		return nil, constants.NewAppError(constants.CodeConflict, "合同当前不可确认付款")
	}
	stage, err := s.stageAt(c, req.StageIndex)
	if err != nil {
		return nil, err
	}

	switch stage.Status {
	case constants.StageDone:
		// Duplicate confirmation: count once.
		return c, nil
	case constants.StageSubmitted:
		// allowed, proceed below
	case constants.StagePending:
		return nil, constants.NewAppError(constants.CodeConflict, "该阶段尚未开始")
	case constants.StageInProgress, constants.StageRejected:
		return nil, constants.NewAppError(constants.CodeConflict, "乙方尚未提交该阶段，不能确认付款")
	default:
		return nil, constants.NewAppError(constants.CodeConflict, "该阶段当前不可确认付款")
	}

	now := time.Now()
	stage.Status = constants.StageDone
	stage.ConfirmedAt = &now

	isLast := req.StageIndex == len(c.Stages)-1
	if isLast {
		c.Status = constants.ContractCompleted
	} else {
		c.Stages[req.StageIndex+1].Status = constants.StageInProgress
		c.Status = constants.ContractInProgress
	}

	if err := s.contracts.Update(c); err != nil {
		return nil, fmt.Errorf("confirm stage: %w", err)
	}
	s.logs.Record(userID, userName, "contract.stage_confirm", "contract", c.ID, fmt.Sprintf("确认第%d阶段「%s」付款", req.StageIndex+1, stage.Name))

	if isLast {
		if err := s.completeRequirement(c); err != nil {
			return nil, err
		}
		s.logs.Record(userID, userName, "contract.complete", "contract", c.ID, "末段付款已确认，合同自动完成")
	}
	return c, nil
}

// RejectStage sends a submitted stage back to Party B for rework (Party A
// only). The contract returns to in_progress and Party B must resubmit.
func (s *ContractService) RejectStage(id uint, req dto.RejectStageRequest, userID uint, userName string) (*model.Contract, error) {
	c, err := s.contracts.FindByID(id)
	if err != nil {
		return nil, err
	}
	if c.PartyAID != userID {
		return nil, constants.ErrForbidden
	}
	if c.Status != constants.ContractInProgress && c.Status != constants.ContractPendingReview {
		return nil, constants.NewAppError(constants.CodeConflict, "合同当前不可驳回")
	}
	stage, err := s.stageAt(c, req.StageIndex)
	if err != nil {
		return nil, err
	}

	switch stage.Status {
	case constants.StageRejected:
		// Duplicate rejection: count once.
		return c, nil
	case constants.StageDone:
		return nil, constants.NewAppError(constants.CodeConflict, "该阶段已确认付款，不能驳回")
	case constants.StagePending:
		return nil, constants.NewAppError(constants.CodeConflict, "该阶段尚未开始，不能驳回")
	case constants.StageInProgress:
		return nil, constants.NewAppError(constants.CodeConflict, "乙方尚未提交该阶段，不能驳回")
	}

	now := time.Now()
	stage.Status = constants.StageRejected
	stage.RejectReason = req.Reason
	stage.RejectedAt = &now
	stage.SubmittedAt = nil
	c.Status = constants.ContractInProgress
	if err := s.contracts.Update(c); err != nil {
		return nil, fmt.Errorf("reject stage: %w", err)
	}
	s.logs.Record(userID, userName, "contract.stage_reject", "contract", c.ID, fmt.Sprintf("驳回第%d阶段「%s」：%s", req.StageIndex+1, stage.Name, req.Reason))
	return c, nil
}

// stageAt returns a pointer to the stage at idx after a bounds check.
func (s *ContractService) stageAt(c *model.Contract, idx int) (*model.ContractStage, error) {
	if idx < 0 || idx >= len(c.Stages) {
		return nil, constants.NewAppError(constants.CodeBadRequest, "无效的阶段序号")
	}
	return &c.Stages[idx], nil
}

// completeRequirement marks the linked requirement completed once the
// contract's final payment is confirmed.
func (s *ContractService) completeRequirement(c *model.Contract) error {
	r, err := s.requirements.FindByID(c.RequirementID)
	if err != nil {
		return fmt.Errorf("load requirement on completion: %w", err)
	}
	r.Status = constants.RequirementCompleted
	if err := s.requirements.Update(r); err != nil {
		return fmt.Errorf("complete requirement on final stage: %w", err)
	}
	return nil
}
