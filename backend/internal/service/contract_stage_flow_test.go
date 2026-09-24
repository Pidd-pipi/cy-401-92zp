package service

import (
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/gigmatch/gigmatch/internal/constants"
	"github.com/gigmatch/gigmatch/internal/dto"
	"github.com/gigmatch/gigmatch/internal/model"
	"github.com/gigmatch/gigmatch/internal/repository"
)

var stageTestSeq uint64

func newStageTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	// A unique in-memory DSN per test avoids cache=shared cross-test leakage.
	dsn := fmt.Sprintf("file:stageflow%d?mode=memory&cache=shared", atomic.AddUint64(&stageTestSeq, 1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Requirement{}, &model.Bid{}, &model.Contract{}, &model.OperationLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

type stageFixture struct {
	db         *gorm.DB
	svc        *ContractService
	reqSvc     *RequirementService
	requester  *model.User
	freelancer *model.User
	contract   *model.Contract
}

func setupStageFixture(t *testing.T) *stageFixture {
	t.Helper()
	db := newStageTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	userRepo := repository.NewUserRepository(db)
	reqRepo := repository.NewRequirementRepository(db)
	bidRepo := repository.NewBidRepository(db)
	contractRepo := repository.NewContractRepository(db)
	logRepo := repository.NewOperationLogRepository(db)

	logSvc := NewOperationLogService(logRepo, logger)
	contractSvc := NewContractService(contractRepo, reqRepo, logSvc, logger)
	reqSvc := NewRequirementService(reqRepo, bidRepo, logSvc, logger)
	bidSvc := NewBidService(bidRepo, reqRepo, logSvc, logger)

	requester := &model.User{Username: "req-stage", PasswordHash: "x", Name: "需求方", Role: constants.RoleRequester}
	freelancer := &model.User{Username: "free-stage", PasswordHash: "x", Name: "自由职业者", Role: constants.RoleFreelancer}
	if err := userRepo.Create(requester); err != nil {
		t.Fatal(err)
	}
	if err := userRepo.Create(freelancer); err != nil {
		t.Fatal(err)
	}

	requirement, err := reqSvc.Create(dto.CreateRequirementRequest{
		Title: "分阶段付款项目", Description: "三段付款", MinBudget: 10000, MaxBudget: 50000, Skills: []string{"Go"},
	}, requester.ID, requester.Name, requester.Role)
	if err != nil {
		t.Fatalf("create requirement: %v", err)
	}
	bid, err := bidSvc.Create(dto.CreateBidRequest{
		RequirementID: requirement.ID, Amount: 10000, DurationDays: 30, Proposal: "ok",
	}, freelancer.ID, freelancer.Name, freelancer.Role)
	if err != nil {
		t.Fatalf("create bid: %v", err)
	}
	contract, err := reqSvc.AcceptBid(requirement.ID, bid.ID, requester.ID, requester.Name, "installments", contractSvc)
	if err != nil {
		t.Fatalf("accept bid: %v", err)
	}

	return &stageFixture{
		db:         db,
		svc:        contractSvc,
		reqSvc:     reqSvc,
		requester:  requester,
		freelancer: freelancer,
		contract:   contract,
	}
}

// Before signing, no stage can be submitted.
func TestStageFlowCannotSubmitBeforeSign(t *testing.T) {
	f := setupStageFixture(t)

	if _, err := f.svc.SubmitStage(f.contract.ID, dto.SubmitStageRequest{StageIndex: 0}, f.freelancer.ID, f.freelancer.Name); err == nil {
		t.Fatal("expected submit before sign to fail")
	}
}

// After signing, only the first stage is active.
func TestStageFlowSignOpensFirstStage(t *testing.T) {
	f := setupStageFixture(t)

	c, err := f.svc.Sign(f.contract.ID, f.freelancer.ID, f.freelancer.Name)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if c.Status != constants.ContractInProgress {
		t.Fatalf("status = %s, want in_progress", c.Status)
	}
	if c.Stages[0].Status != constants.StageInProgress {
		t.Fatalf("stage0 = %s, want in_progress", c.Stages[0].Status)
	}
	if c.Stages[1].Status != constants.StagePending || c.Stages[2].Status != constants.StagePending {
		t.Fatalf("later stages should stay pending, got %s/%s", c.Stages[1].Status, c.Stages[2].Status)
	}
}

// A full happy path: submit -> confirm per stage; contract auto-completes
// on the last confirmation and the requirement is completed.
func TestStageFlowFullLifecycleAutoCompletes(t *testing.T) {
	f := setupStageFixture(t)
	ctx := f
	contractID := ctx.contract.ID

	if _, err := ctx.svc.Sign(contractID, ctx.requester.ID, ctx.requester.Name); err != nil {
		t.Fatalf("sign: %v", err)
	}

	// Stages 0 and 1 keep the contract in_progress after confirmation.
	for i := 0; i < 2; i++ {
		submitted, err := ctx.svc.SubmitStage(contractID, dto.SubmitStageRequest{StageIndex: i, Note: "交付"}, ctx.freelancer.ID, ctx.freelancer.Name)
		if err != nil {
			t.Fatalf("submit stage %d: %v", i, err)
		}
		if submitted.Stages[i].Status != constants.StageSubmitted {
			t.Fatalf("stage %d = %s, want submitted", i, submitted.Stages[i].Status)
		}
		if submitted.Status != constants.ContractPendingReview {
			t.Fatalf("contract = %s after submit, want pending_review", submitted.Status)
		}

		confirmed, err := ctx.svc.ConfirmStage(contractID, dto.ConfirmStageRequest{StageIndex: i}, ctx.requester.ID, ctx.requester.Name)
		if err != nil {
			t.Fatalf("confirm stage %d: %v", i, err)
		}
		if confirmed.Stages[i].Status != constants.StageDone || confirmed.Stages[i].ConfirmedAt == nil {
			t.Fatalf("stage %d not marked done with timestamp", i)
		}
		if confirmed.Stages[i+1].Status != constants.StageInProgress {
			t.Fatalf("stage %d = %s, want in_progress after prior confirmation", i+1, confirmed.Stages[i+1].Status)
		}
		if confirmed.Status != constants.ContractInProgress {
			t.Fatalf("contract = %s mid-flow, want in_progress", confirmed.Status)
		}
	}

	// Final stage.
	if _, err := ctx.svc.SubmitStage(contractID, dto.SubmitStageRequest{StageIndex: 2}, ctx.freelancer.ID, ctx.freelancer.Name); err != nil {
		t.Fatalf("submit stage 2: %v", err)
	}
	completed, err := ctx.svc.ConfirmStage(contractID, dto.ConfirmStageRequest{StageIndex: 2}, ctx.requester.ID, ctx.requester.Name)
	if err != nil {
		t.Fatalf("confirm stage 2: %v", err)
	}
	if completed.Status != constants.ContractCompleted {
		t.Fatalf("contract = %s, want completed", completed.Status)
	}
	for i, st := range completed.Stages {
		if st.Status != constants.StageDone {
			t.Fatalf("stage %d = %s, want all done", i, st.Status)
		}
	}

	// Requirement is completed automatically.
	req, err := ctx.reqSvc.Get(completed.RequirementID)
	if err != nil {
		t.Fatalf("load requirement: %v", err)
	}
	if req.Status != constants.RequirementCompleted {
		t.Fatalf("requirement = %s, want completed", req.Status)
	}
}

// Party B cannot skip ahead to a later stage.
func TestStageFlowCannotSkipAhead(t *testing.T) {
	f := setupStageFixture(t)
	if _, err := f.svc.Sign(f.contract.ID, f.requester.ID, f.requester.Name); err != nil {
		t.Fatal(err)
	}
	// Stage 1 is still pending because stage 0 is unconfirmed.
	if _, err := f.svc.SubmitStage(f.contract.ID, dto.SubmitStageRequest{StageIndex: 1}, f.freelancer.ID, f.freelancer.Name); err == nil {
		t.Fatal("expected skip-ahead submit to fail")
	}
	if _, err := f.svc.ConfirmStage(f.contract.ID, dto.ConfirmStageRequest{StageIndex: 1}, f.requester.ID, f.requester.Name); err == nil {
		t.Fatal("expected skip-ahead confirm to fail")
	}
}

// Reject sends the stage back; Party B resubmits and it can be confirmed.
func TestStageFlowRejectAndResubmit(t *testing.T) {
	f := setupStageFixture(t)
	id := f.contract.ID
	if _, err := f.svc.Sign(id, f.requester.ID, f.requester.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SubmitStage(id, dto.SubmitStageRequest{StageIndex: 0, Note: "初稿"}, f.freelancer.ID, f.freelancer.Name); err != nil {
		t.Fatal(err)
	}

	rejected, err := f.svc.RejectStage(id, dto.RejectStageRequest{StageIndex: 0, Reason: "缺少接口文档"}, f.requester.ID, f.requester.Name)
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rejected.Stages[0].Status != constants.StageRejected || rejected.Stages[0].RejectReason != "缺少接口文档" {
		t.Fatalf("stage0 = %+v, want rejected with reason", rejected.Stages[0])
	}
	if rejected.Stages[0].SubmittedAt != nil {
		t.Fatal("submittedAt should be cleared on rejection")
	}
	if rejected.Status != constants.ContractInProgress {
		t.Fatalf("contract = %s after reject, want in_progress", rejected.Status)
	}

	// Cannot confirm a rejected stage.
	if _, err := f.svc.ConfirmStage(id, dto.ConfirmStageRequest{StageIndex: 0}, f.requester.ID, f.requester.Name); err == nil {
		t.Fatal("expected confirm of rejected stage to fail")
	}

	// Party B reworks and resubmits.
	resubmitted, err := f.svc.SubmitStage(id, dto.SubmitStageRequest{StageIndex: 0, Note: "已补文档"}, f.freelancer.ID, f.freelancer.Name)
	if err != nil {
		t.Fatalf("resubmit: %v", err)
	}
	if resubmitted.Stages[0].Status != constants.StageSubmitted {
		t.Fatalf("stage0 = %s, want submitted", resubmitted.Stages[0].Status)
	}
	if resubmitted.Stages[0].RejectReason != "" {
		t.Fatal("reject reason should be cleared on resubmit")
	}

	confirmed, err := f.svc.ConfirmStage(id, dto.ConfirmStageRequest{StageIndex: 0}, f.requester.ID, f.requester.Name)
	if err != nil {
		t.Fatalf("confirm after resubmit: %v", err)
	}
	if confirmed.Stages[0].Status != constants.StageDone {
		t.Fatalf("stage0 = %s, want done", confirmed.Stages[0].Status)
	}
}

// Repeated submit and repeated confirm count once (idempotent, no error,
// no state corruption).
func TestStageFlowIdempotentRepeatedActions(t *testing.T) {
	f := setupStageFixture(t)
	id := f.contract.ID
	if _, err := f.svc.Sign(id, f.requester.ID, f.requester.Name); err != nil {
		t.Fatal(err)
	}
	first, err := f.svc.SubmitStage(id, dto.SubmitStageRequest{StageIndex: 0, Note: "a"}, f.freelancer.ID, f.freelancer.Name)
	if err != nil {
		t.Fatal(err)
	}
	firstSubmittedAt := first.Stages[0].SubmittedAt

	// Repeated submit while waiting for confirmation is a no-op and must
	// not overwrite the note/timestamp.
	again, err := f.svc.SubmitStage(id, dto.SubmitStageRequest{StageIndex: 0, Note: "b"}, f.freelancer.ID, f.freelancer.Name)
	if err != nil {
		t.Fatalf("duplicate submit should be no-op, got %v", err)
	}
	if again.Stages[0].Note != "a" || !again.Stages[0].SubmittedAt.Equal(*firstSubmittedAt) {
		t.Fatal("duplicate submit unexpectedly changed stage data")
	}

	confirmed, err := f.svc.ConfirmStage(id, dto.ConfirmStageRequest{StageIndex: 0}, f.requester.ID, f.requester.Name)
	if err != nil {
		t.Fatal(err)
	}
	confirmedAt := confirmed.Stages[0].ConfirmedAt

	// Repeated confirm is a no-op and must not advance twice.
	reconfirm, err := f.svc.ConfirmStage(id, dto.ConfirmStageRequest{StageIndex: 0}, f.requester.ID, f.requester.Name)
	if err != nil {
		t.Fatalf("duplicate confirm should be no-op, got %v", err)
	}
	if !reconfirm.Stages[0].ConfirmedAt.Equal(*confirmedAt) {
		t.Fatal("duplicate confirm unexpectedly changed timestamp")
	}
	if reconfirm.Stages[1].Status != constants.StageInProgress {
		t.Fatalf("stage1 = %s, want in_progress exactly once", reconfirm.Stages[1].Status)
	}

	// Repeated reject is a no-op too.
	if _, err := f.svc.SubmitStage(id, dto.SubmitStageRequest{StageIndex: 1}, f.freelancer.ID, f.freelancer.Name); err != nil {
		t.Fatal(err)
	}
	r1, err := f.svc.RejectStage(id, dto.RejectStageRequest{StageIndex: 1, Reason: "first"}, f.requester.ID, f.requester.Name)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := f.svc.RejectStage(id, dto.RejectStageRequest{StageIndex: 1, Reason: "second"}, f.requester.ID, f.requester.Name)
	if err != nil {
		t.Fatalf("duplicate reject should be no-op, got %v", err)
	}
	if r2.Stages[1].RejectReason != "first" || !r2.Stages[1].RejectedAt.Equal(*r1.Stages[1].RejectedAt) {
		t.Fatal("duplicate reject unexpectedly changed stage data")
	}
}

// Role checks: only Party B submits; only Party A confirms/rejects.
func TestStageFlowRoleRestrictions(t *testing.T) {
	f := setupStageFixture(t)
	id := f.contract.ID
	if _, err := f.svc.Sign(id, f.requester.ID, f.requester.Name); err != nil {
		t.Fatal(err)
	}

	// Party A cannot submit.
	if _, err := f.svc.SubmitStage(id, dto.SubmitStageRequest{StageIndex: 0}, f.requester.ID, f.requester.Name); err == nil {
		t.Fatal("party A submit should be forbidden")
	}
	if _, err := f.svc.SubmitStage(id, dto.SubmitStageRequest{StageIndex: 0}, f.freelancer.ID, f.freelancer.Name); err != nil {
		t.Fatal(err)
	}
	// Party B cannot confirm or reject.
	if _, err := f.svc.ConfirmStage(id, dto.ConfirmStageRequest{StageIndex: 0}, f.freelancer.ID, f.freelancer.Name); err == nil {
		t.Fatal("party B confirm should be forbidden")
	}
	if _, err := f.svc.RejectStage(id, dto.RejectStageRequest{StageIndex: 0, Reason: "x"}, f.freelancer.ID, f.freelancer.Name); err == nil {
		t.Fatal("party B reject should be forbidden")
	}
}

// Out-of-range stage index is rejected.
func TestStageFlowInvalidIndex(t *testing.T) {
	f := setupStageFixture(t)
	id := f.contract.ID
	if _, err := f.svc.SubmitStage(id, dto.SubmitStageRequest{StageIndex: 9}, f.freelancer.ID, f.freelancer.Name); err == nil {
		t.Fatal("expected invalid stage index to fail")
	}
}

// No stage actions after the contract is completed.
func TestStageFlowLockedAfterCompletion(t *testing.T) {
	f := setupStageFixture(t)
	id := f.contract.ID
	if _, err := f.svc.Sign(id, f.requester.ID, f.requester.Name); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := f.svc.SubmitStage(id, dto.SubmitStageRequest{StageIndex: i}, f.freelancer.ID, f.freelancer.Name); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		if _, err := f.svc.ConfirmStage(id, dto.ConfirmStageRequest{StageIndex: i}, f.requester.ID, f.requester.Name); err != nil {
			t.Fatalf("confirm %d: %v", i, err)
		}
	}
	if _, err := f.svc.SubmitStage(id, dto.SubmitStageRequest{StageIndex: 0}, f.freelancer.ID, f.freelancer.Name); err == nil {
		t.Fatal("submit after completion should fail")
	}
	if _, err := f.svc.RejectStage(id, dto.RejectStageRequest{StageIndex: 0, Reason: "x"}, f.requester.ID, f.requester.Name); err == nil {
		t.Fatal("reject after completion should fail")
	}
}
