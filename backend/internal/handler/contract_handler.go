package handler

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/gigmatch/gigmatch/internal/constants"
	"github.com/gigmatch/gigmatch/internal/dto"
	"github.com/gigmatch/gigmatch/internal/middleware"
	"github.com/gigmatch/gigmatch/internal/service"
	"github.com/gigmatch/gigmatch/internal/util"
)

// ContractHandler exposes contract endpoints.
type ContractHandler struct {
	svc    *service.ContractService
	logger *slog.Logger
}

// NewContractHandler builds a ContractHandler.
func NewContractHandler(svc *service.ContractService, logger *slog.Logger) *ContractHandler {
	return &ContractHandler{svc: svc, logger: logger}
}

// List handles GET /contracts.
func (h *ContractHandler) List(c *gin.Context) {
	u := middleware.GetCurrentUser(c)
	contracts, err := h.svc.ListByParty(u.ID)
	if err != nil {
		util.Fail(c, err)
		return
	}
	util.OK(c, contracts)
}

// Get handles GET /contracts/:id.
func (h *ContractHandler) Get(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	contract, err := h.svc.Get(id)
	if err != nil {
		util.Fail(c, err)
		return
	}
	util.OK(c, contract)
}

// Sign handles POST /contracts/:id/sign.
func (h *ContractHandler) Sign(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	u := middleware.GetCurrentUser(c)
	contract, err := h.svc.Sign(id, u.ID, u.Name)
	if err != nil {
		util.Fail(c, err)
		return
	}
	util.OK(c, contract)
}

// SubmitStage handles POST /contracts/:id/stages/:stageNo/submit.
func (h *ContractHandler) SubmitStage(c *gin.Context) {
	id, stageNo, ok := parseContractStage(c)
	if !ok {
		return
	}
	u := middleware.GetCurrentUser(c)
	contract, err := h.svc.SubmitStage(id, stageNo, u.ID, u.Name)
	if err != nil {
		util.Fail(c, err)
		return
	}
	util.OK(c, contract)
}

// ConfirmStage handles POST /contracts/:id/stages/:stageNo/confirm.
func (h *ContractHandler) ConfirmStage(c *gin.Context) {
	id, stageNo, ok := parseContractStage(c)
	if !ok {
		return
	}
	u := middleware.GetCurrentUser(c)
	contract, err := h.svc.ConfirmStage(id, stageNo, u.ID, u.Name)
	if err != nil {
		util.Fail(c, err)
		return
	}
	util.OK(c, contract)
}

// RejectStage handles POST /contracts/:id/stages/:stageNo/reject.
func (h *ContractHandler) RejectStage(c *gin.Context) {
	id, stageNo, ok := parseContractStage(c)
	if !ok {
		return
	}
	var req dto.RejectStageRequest
	if !util.BindAndValidate(c, &req) {
		return
	}
	u := middleware.GetCurrentUser(c)
	contract, err := h.svc.RejectStage(id, stageNo, req.Reason, u.ID, u.Name)
	if err != nil {
		util.Fail(c, err)
		return
	}
	util.OK(c, contract)
}

// parseContractStage extracts contract id and 1-based stage number.
func parseContractStage(c *gin.Context) (uint, int, bool) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		return 0, 0, false
	}
	n, ok := parseUintParam(c, "stageNo")
	if !ok || n < 1 {
		util.Fail(c, invalidStageError())
		return 0, 0, false
	}
	return id, int(n), true
}

func invalidStageError() error {
	return constants.NewAppError(constants.CodeBadRequest, "无效的阶段编号")
}
