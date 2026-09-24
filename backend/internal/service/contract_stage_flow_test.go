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

var stageTestDBCounter atomic.Uint64

func newIsolatedTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:stage_flow_%d?mode=memory&cache=shared", stageTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Requirement{}, &model.Bid{}, &model.Contract{}, &model.OperationLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func newStageFlowEnv(t *testing.T) (*ContractService, *RequirementService, *BidService, *model.User, *model.User, *model.Contract) {
	t.Helper()
	db := newIsolatedTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	userRepo := repository.NewUserRepository(db)
	reqRepo := repository.NewRequirementRepository(db)
	bidRepo := repository.NewBidRepository(db)
	contractRepo := repository.NewContractRepository(db)
	logRepo := repository.NewOperationLogRepository(db)

	requester := &model.User{Username: "stage_req", PasswordHash: "x", Name: "需求方", Role: constants.RoleRequester}
	freelancer := &model.User{Username: "stage_free", PasswordHash: "x", Name: "自由职业者", Role: constants.RoleFreelancer}
	if err := userRepo.Create(requester); err != nil {
		t.Fatal(err)
	}
	if err := userRepo.Create(freelancer); err != nil {
		t.Fatal(err)
	}

	logSvc := NewOperationLogService(logRepo, logger)
	contractSvc := NewContractService(contractRepo, logSvc, logger)
	reqSvc := NewRequirementService(reqRepo, bidRepo, logSvc, logger)
	bidSvc := NewBidService(bidRepo, reqRepo, logSvc, logger)

	requirement, err := reqSvc.Create(dto.CreateRequirementRequest{
		Title: "分阶段付款测试", Description: "三段付款", MinBudget: 10000, MaxBudget: 50000, Skills: []string{"Go"},
	}, requester.ID, requester.Name, requester.Role)
	if err != nil {
		t.Fatalf("create requirement: %v", err)
	}
	bid, err := bidSvc.Create(dto.CreateBidRequest{
		RequirementID: requirement.ID, Amount: 10000, DurationDays: 30, Proposal: "按三段执行",
	}, freelancer.ID, freelancer.Name, freelancer.Role)
	if err != nil {
		t.Fatalf("create bid: %v", err)
	}
	contract, err := reqSvc.AcceptBid(requirement.ID, bid.ID, requester.ID, requester.Name, "installments", contractSvc)
	if err != nil {
		t.Fatalf("accept bid: %v", err)
	}
	return contractSvc, reqSvc, bidSvc, requester, freelancer, contract
}

func stageStatuses(c *model.Contract) []string {
	out := make([]string, 0, len(c.Stages))
	for _, st := range c.Stages {
		out = append(out, st.Status)
	}
	return out
}

func TestStagesAllPendingBeforeSign(t *testing.T) {
	svc, _, _, _, _, c := newStageFlowEnv(t)
	got, err := svc.Get(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, st := range got.Stages {
		if st.Status != constants.StagePending {
			t.Fatalf("stage %d status = %s, want pending before sign", i+1, st.Status)
		}
	}
}

func TestSignOpensFirstStage(t *testing.T) {
	svc, _, _, req, free, c := newStageFlowEnv(t)

	// Either party can sign; Party A signing here opens stage 1.
	got, err := svc.Sign(c.ID, req.ID, req.Name)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if got.Status != constants.ContractInProgress {
		t.Fatalf("contract status = %s, want in_progress", got.Status)
	}
	statuses := stageStatuses(got)
	want := []string{constants.StageInProgress, constants.StagePending, constants.StagePending}
	for i := range want {
		if statuses[i] != want[i] {
			t.Fatalf("after sign statuses = %v, want %v", statuses, want)
		}
	}

	// Signing again is rejected.
	if _, err := svc.Sign(c.ID, free.ID, free.Name); err == nil {
		t.Fatal("second sign should be rejected")
	}
}

func TestStageLifecycleHappyPath(t *testing.T) {
	svc, _, _, req, free, c := newStageFlowEnv(t)
	if _, err := svc.Sign(c.ID, req.ID, req.Name); err != nil {
		t.Fatal(err)
	}

	// Stage 1: freelancer submits, requester confirms payment.
	submitted, err := svc.SubmitStage(c.ID, 1, free.ID, free.Name)
	if err != nil {
		t.Fatalf("submit stage1: %v", err)
	}
	if submitted.Status != constants.ContractPendingReview || submitted.Stages[0].Status != constants.StageSubmitted {
		t.Fatalf("after submit contract=%s stage1=%s", submitted.Status, submitted.Stages[0].Status)
	}
	if submitted.Stages[0].SubmittedAt == "" {
		t.Fatal("submittedAt should be recorded")
	}

	confirmed, err := svc.ConfirmStage(c.ID, 1, req.ID, req.Name)
	if err != nil {
		t.Fatalf("confirm stage1: %v", err)
	}
	if confirmed.Status != constants.ContractInProgress {
		t.Fatalf("contract after stage1 = %s, want in_progress", confirmed.Status)
	}
	statuses := stageStatuses(confirmed)
	want := []string{constants.StagePaid, constants.StageInProgress, constants.StagePending}
	for i := range want {
		if statuses[i] != want[i] {
			t.Fatalf("after stage1 confirm statuses = %v, want %v", statuses, want)
		}
	}
	if confirmed.Stages[0].PaidAt == "" {
		t.Fatal("paidAt should be recorded")
	}

	// Stage 2.
	if _, err := svc.SubmitStage(c.ID, 2, free.ID, free.Name); err != nil {
		t.Fatalf("submit stage2: %v", err)
	}
	if _, err := svc.ConfirmStage(c.ID, 2, req.ID, req.Name); err != nil {
		t.Fatalf("confirm stage2: %v", err)
	}

	// Stage 3: confirming the final stage completes the contract automatically.
	if _, err := svc.SubmitStage(c.ID, 3, free.ID, free.Name); err != nil {
		t.Fatalf("submit stage3: %v", err)
	}
	done, err := svc.ConfirmStage(c.ID, 3, req.ID, req.Name)
	if err != nil {
		t.Fatalf("confirm stage3: %v", err)
	}
	if done.Status != constants.ContractCompleted {
		t.Fatalf("contract after final stage = %s, want completed", done.Status)
	}
	for i, st := range done.Stages {
		if st.Status != constants.StagePaid {
			t.Fatalf("stage %d = %s, want paid", i+1, st.Status)
		}
	}
}

func TestCannotSkipUnpaidStage(t *testing.T) {
	svc, _, _, req, free, c := newStageFlowEnv(t)
	if _, err := svc.Sign(c.ID, req.ID, req.Name); err != nil {
		t.Fatal(err)
	}

	// Freelancer cannot submit stage 2 while stage 1 is still in progress.
	if _, err := svc.SubmitStage(c.ID, 2, free.ID, free.Name); err == nil {
		t.Fatal("submit stage2 before stage1 paid should fail")
	}
	// Party A cannot confirm later stages either.
	if _, err := svc.ConfirmStage(c.ID, 3, req.ID, req.Name); err == nil {
		t.Fatal("confirm stage3 before earlier stages paid should fail")
	}
}

func TestCannotOperateBeforeSign(t *testing.T) {
	svc, _, _, req, free, c := newStageFlowEnv(t)
	if _, err := svc.SubmitStage(c.ID, 1, free.ID, free.Name); err == nil {
		t.Fatal("submit before sign should fail")
	}
	if _, err := svc.ConfirmStage(c.ID, 1, req.ID, req.Name); err == nil {
		t.Fatal("confirm before sign should fail")
	}
}

func TestPartyPermissions(t *testing.T) {
	svc, _, _, req, free, c := newStageFlowEnv(t)
	if _, err := svc.Sign(c.ID, req.ID, req.Name); err != nil {
		t.Fatal(err)
	}

	// Requester cannot submit.
	if _, err := svc.SubmitStage(c.ID, 1, req.ID, req.Name); err == nil {
		t.Fatal("party A submit should fail")
	}
	// Freelancer cannot confirm.
	if _, err := svc.ConfirmStage(c.ID, 1, free.ID, free.Name); err == nil {
		t.Fatal("party B confirm should fail")
	}
	// Freelancer cannot reject.
	if _, err := svc.RejectStage(c.ID, 1, "不符", free.ID, free.Name); err == nil {
		t.Fatal("party B reject should fail")
	}
}

func TestConfirmOnlyAfterSubmit(t *testing.T) {
	svc, _, _, req, _, c := newStageFlowEnv(t)
	if _, err := svc.Sign(c.ID, req.ID, req.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ConfirmStage(c.ID, 1, req.ID, req.Name); err == nil {
		t.Fatal("confirming a stage that has not been submitted should fail")
	}
}

func TestRejectAndResubmit(t *testing.T) {
	svc, _, _, req, free, c := newStageFlowEnv(t)
	if _, err := svc.Sign(c.ID, req.ID, req.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitStage(c.ID, 1, free.ID, free.Name); err != nil {
		t.Fatal(err)
	}

	rejected, err := svc.RejectStage(c.ID, 1, "交付与需求不符", req.ID, req.Name)
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rejected.Status != constants.ContractInProgress || rejected.Stages[0].Status != constants.StageRejected {
		t.Fatalf("after reject contract=%s stage=%s", rejected.Status, rejected.Stages[0].Status)
	}
	if rejected.Stages[0].RejectReason != "交付与需求不符" {
		t.Fatalf("reject reason = %q", rejected.Stages[0].RejectReason)
	}

	// Reject without a reason is invalid.
	if _, err := svc.RejectStage(c.ID, 1, "  ", req.ID, req.Name); err == nil {
		t.Fatal("blank reject reason should fail")
	}

	// Freelancer adjusts and resubmits; then confirmation works.
	if _, err := svc.SubmitStage(c.ID, 1, free.ID, free.Name); err != nil {
		t.Fatalf("resubmit: %v", err)
	}
	loaded, err := svc.Get(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Stages[0].Status != constants.StageSubmitted || loaded.Stages[0].RejectReason != "" {
		t.Fatalf("after resubmit stage=%s reason=%q", loaded.Stages[0].Status, loaded.Stages[0].RejectReason)
	}
	if _, err := svc.ConfirmStage(c.ID, 1, req.ID, req.Name); err != nil {
		t.Fatalf("confirm after resubmit: %v", err)
	}
}

func TestRejectOnlySubmittedStage(t *testing.T) {
	svc, _, _, req, free, c := newStageFlowEnv(t)
	if _, err := svc.Sign(c.ID, req.ID, req.Name); err != nil {
		t.Fatal(err)
	}
	// An in-progress (not yet submitted) stage cannot be rejected.
	if _, err := svc.RejectStage(c.ID, 1, "不符", req.ID, req.Name); err == nil {
		t.Fatal("reject before submit should fail")
	}
	if _, err := svc.SubmitStage(c.ID, 1, free.ID, free.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ConfirmStage(c.ID, 1, req.ID, req.Name); err != nil {
		t.Fatal(err)
	}
	// A paid stage cannot be rejected.
	if _, err := svc.RejectStage(c.ID, 1, "不符", req.ID, req.Name); err == nil {
		t.Fatal("reject after paid should fail")
	}
}

func TestDuplicateSubmissionAndConfirmationCountOnce(t *testing.T) {
	svc, _, _, req, free, c := newStageFlowEnv(t)
	if _, err := svc.Sign(c.ID, req.ID, req.Name); err != nil {
		t.Fatal(err)
	}

	first, err := svc.SubmitStage(c.ID, 1, free.ID, free.Name)
	if err != nil {
		t.Fatal(err)
	}
	submittedAt := first.Stages[0].SubmittedAt

	// Duplicate submission is a no-op: state and timestamp stay unchanged.
	again, err := svc.SubmitStage(c.ID, 1, free.ID, free.Name)
	if err != nil {
		t.Fatalf("duplicate submit should be idempotent: %v", err)
	}
	if again.Stages[0].Status != constants.StageSubmitted || again.Stages[0].SubmittedAt != submittedAt {
		t.Fatal("duplicate submission must not change the stage")
	}

	confirmed, err := svc.ConfirmStage(c.ID, 1, req.ID, req.Name)
	if err != nil {
		t.Fatal(err)
	}
	paidAt := confirmed.Stages[0].PaidAt

	// Duplicate confirmation is a no-op and must not re-open / complete anything.
	reconfirmed, err := svc.ConfirmStage(c.ID, 1, req.ID, req.Name)
	if err != nil {
		t.Fatalf("duplicate confirm should be idempotent: %v", err)
	}
	if reconfirmed.Stages[0].Status != constants.StagePaid || reconfirmed.Stages[0].PaidAt != paidAt {
		t.Fatal("duplicate confirmation must not change the stage")
	}
	if reconfirmed.Stages[1].Status != constants.StageInProgress {
		t.Fatalf("stage2 = %s, want in_progress after single effective confirmation", reconfirmed.Stages[1].Status)
	}
}

func TestInvalidStageNumber(t *testing.T) {
	svc, _, _, req, free, c := newStageFlowEnv(t)
	if _, err := svc.Sign(c.ID, req.ID, req.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitStage(c.ID, 0, free.ID, free.Name); err == nil {
		t.Fatal("stage 0 should be invalid")
	}
	if _, err := svc.SubmitStage(c.ID, 4, free.ID, free.Name); err == nil {
		t.Fatal("stage 4 should be invalid for a 3-stage contract")
	}
}
