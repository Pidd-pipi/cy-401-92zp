package service

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gigmatch/gigmatch/internal/constants"
	"github.com/gigmatch/gigmatch/internal/model"
	"github.com/gigmatch/gigmatch/internal/repository"
)

// ContractService manages contracts.
type ContractService struct {
	contracts *repository.ContractRepository
	logs      *OperationLogService
	logger    *slog.Logger
}

// NewContractService builds a ContractService.
func NewContractService(contracts *repository.ContractRepository, logs *OperationLogService, logger *slog.Logger) *ContractService {
	return &ContractService{contracts: contracts, logs: logs, logger: logger}
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
	// All stages start pending; the first one opens only after both parties sign.
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

// Sign confirms a contract by either party. Signing opens the first stage.
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

// SubmitStage is used by Party B (freelancer) to hand in the deliverable of the
// current stage, asking Party A to confirm payment. Re-submitting a submitted
// stage is a no-op so duplicate requests only count once. A later stage cannot
// be submitted before earlier stages are paid.
func (s *ContractService) SubmitStage(id uint, stageNo int, userID uint, userName string) (*model.Contract, error) {
	c, stage, err := s.loadStage(id, stageNo, userID, false)
	if err != nil {
		return nil, err
	}
	if c.Status == constants.ContractTerminated {
		return nil, constants.NewAppError(constants.CodeConflict, "合同已终止，不能操作阶段")
	}
	if c.Status != constants.ContractInProgress && c.Status != constants.ContractPendingReview {
		return nil, constants.NewAppError(constants.CodeConflict, "合同当前不在执行中，无法提交阶段")
	}

	switch stage.Status {
	case constants.StageInProgress, constants.StageRejected:
		stage.Status = constants.StageSubmitted
		stage.RejectReason = ""
		stage.SubmittedAt = nowText()
		c.Status = constants.ContractPendingReview
		s.logs.Record(userID, userName, "contract.stage.submit", "contract", c.ID,
			fmt.Sprintf("提交第%d阶段「%s」交付", stageNo, stage.Name))
	case constants.StageSubmitted:
		// Idempotent: duplicate submission counts once.
		return c, nil
	case constants.StagePending:
		return nil, constants.NewAppError(constants.CodeConflict, "上一阶段尚未确认付款，不能提前提交本阶段")
	case constants.StagePaid:
		// Idempotent: already confirmed, nothing to do.
		return c, nil
	default:
		return nil, constants.NewAppError(constants.CodeConflict, "当前阶段状态不可提交")
	}

	if err := s.contracts.Update(c); err != nil {
		return nil, fmt.Errorf("submit stage: %w", err)
	}
	return c, nil
}

// ConfirmStage is used by Party A (requester) to confirm the delivery of a
// submitted stage and release its payment. The next stage then opens; when the
// final stage is confirmed the contract completes automatically. Confirming an
// already paid stage is a no-op so duplicate clicks only count once.
func (s *ContractService) ConfirmStage(id uint, stageNo int, userID uint, userName string) (*model.Contract, error) {
	c, stage, err := s.loadStage(id, stageNo, userID, true)
	if err != nil {
		return nil, err
	}
	if c.Status == constants.ContractTerminated {
		return nil, constants.NewAppError(constants.CodeConflict, "合同已终止，不能操作阶段")
	}

	switch stage.Status {
	case constants.StageSubmitted:
		stage.Status = constants.StagePaid
		stage.RejectReason = ""
		stage.PaidAt = nowText()
		s.logs.Record(userID, userName, "contract.stage.confirm", "contract", c.ID,
			fmt.Sprintf("确认第%d阶段「%s」并付款", stageNo, stage.Name))
	case constants.StagePaid:
		// Idempotent: duplicate confirmation counts once.
		return c, nil
	case constants.StagePending, constants.StageInProgress:
		return nil, constants.NewAppError(constants.CodeConflict, "乙方尚未提交本阶段交付，不能确认付款")
	case constants.StageRejected:
		return nil, constants.NewAppError(constants.CodeConflict, "本阶段已驳回，需等待乙方重新提交")
	default:
		return nil, constants.NewAppError(constants.CodeConflict, "当前阶段状态不可确认")
	}

	// Open the next stage, or auto-complete the contract after the final one.
	if stageNo < len(c.Stages) {
		c.Stages[stageNo].Status = constants.StageInProgress
		c.Status = constants.ContractInProgress
	} else {
		c.Status = constants.ContractCompleted
	}

	if err := s.contracts.Update(c); err != nil {
		return nil, fmt.Errorf("confirm stage: %w", err)
	}
	if c.Status == constants.ContractCompleted {
		s.logs.Record(userID, userName, "contract.complete", "contract", c.ID, "末段款项确认完成，合同自动完成")
	}
	return c, nil
}

// RejectStage is used by Party A to reject a submitted stage whose deliverable
// does not match the contract. Party B adjusts and submits again.
func (s *ContractService) RejectStage(id uint, stageNo int, reason string, userID uint, userName string) (*model.Contract, error) {
	c, stage, err := s.loadStage(id, stageNo, userID, true)
	if err != nil {
		return nil, err
	}
	if reason = strings.TrimSpace(reason); reason == "" {
		return nil, constants.NewAppError(constants.CodeBadRequest, "请填写驳回原因")
	}
	if c.Status == constants.ContractTerminated {
		return nil, constants.NewAppError(constants.CodeConflict, "合同已终止，不能操作阶段")
	}

	switch stage.Status {
	case constants.StageSubmitted:
		stage.Status = constants.StageRejected
		stage.RejectReason = reason
		c.Status = constants.ContractInProgress
		s.logs.Record(userID, userName, "contract.stage.reject", "contract", c.ID,
			fmt.Sprintf("驳回第%d阶段「%s」：%s", stageNo, stage.Name, reason))
	case constants.StageRejected:
		// Idempotent on state; refresh the reason for the freelancer, no
		// extra transition or log entry.
		stage.RejectReason = reason
		if err := s.contracts.Update(c); err != nil {
			return nil, fmt.Errorf("reject stage: %w", err)
		}
		return c, nil
	case constants.StagePaid:
		return nil, constants.NewAppError(constants.CodeConflict, "本阶段款项已确认，不能驳回")
	default:
		return nil, constants.NewAppError(constants.CodeConflict, "仅已提交的阶段可以驳回")
	}

	if err := s.contracts.Update(c); err != nil {
		return nil, fmt.Errorf("reject stage: %w", err)
	}
	return c, nil
}

// loadStage fetches a contract and the stage (stageNo is 1-based) while
// enforcing party permission and ordering: an earlier unpaid stage blocks
// access to later ones.
func (s *ContractService) loadStage(id uint, stageNo int, userID uint, partyA bool) (*model.Contract, *model.ContractStage, error) {
	if stageNo < 1 {
		return nil, nil, constants.NewAppError(constants.CodeBadRequest, "无效的阶段编号")
	}
	c, err := s.contracts.FindByID(id)
	if err != nil {
		return nil, nil, err
	}
	if partyA {
		if c.PartyAID != userID {
			return nil, nil, constants.ErrForbidden
		}
	} else {
		if c.PartyBID != userID {
			return nil, nil, constants.ErrForbidden
		}
	}
	if c.Status == constants.ContractPendingSignature {
		return nil, nil, constants.NewAppError(constants.CodeConflict, "合同尚未签署")
	}
	if stageNo > len(c.Stages) {
		return nil, nil, constants.NewAppError(constants.CodeBadRequest, "无效的阶段编号")
	}
	// Stages are strictly sequential: every earlier stage must be paid.
	for i := 0; i < stageNo-1; i++ {
		if c.Stages[i].Status != constants.StagePaid {
			return nil, nil, constants.NewAppError(constants.CodeConflict, "前面的阶段尚未确认付款，不能操作本阶段")
		}
	}
	return c, &c.Stages[stageNo-1], nil
}

func nowText() string {
	return time.Now().Format(time.RFC3339)
}
