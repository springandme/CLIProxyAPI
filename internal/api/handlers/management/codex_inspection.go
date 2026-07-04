package management

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/codexinspection"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func (h *Handler) GetCodexInspectionConfig(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler unavailable"})
		return
	}
	h.mu.Lock()
	cfg := config.NormalizeCodexInspectionConfig(h.cfg.CodexInspection, config.DefaultCodexInspectionConfig())
	h.mu.Unlock()
	c.JSON(http.StatusOK, cfg)
}

func (h *Handler) PutCodexInspectionConfig(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler unavailable"})
		return
	}
	var req config.CodexInspectionConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if err := config.ValidateCodexInspectionConfig(req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	h.mu.Lock()
	h.cfg.CodexInspection = config.NormalizeCodexInspectionConfig(req, config.DefaultCodexInspectionConfig())
	snapshot, ok := h.saveConfigAndSnapshotLocked(c)
	h.mu.Unlock()
	if !ok {
		return
	}
	h.reloadConfigAfterManagementSaveAsync(c.Request.Context(), snapshot)
	c.JSON(http.StatusOK, h.cfg.CodexInspection)
}

func (h *Handler) ListCodexInspectionRuns(c *gin.Context) {
	service := h.getCodexInspectionService()
	if service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "codex inspection service unavailable"})
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	runs, err := service.ListRuns(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": runs})
}

func (h *Handler) ListCodexInspectionCooldowns(c *gin.Context) {
	service := h.getCodexInspectionService()
	if service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "codex inspection service unavailable"})
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	includeResolved := strings.EqualFold(c.Query("includeResolved"), "true") || c.Query("includeResolved") == "1"
	items, err := service.ListCooldowns(c.Request.Context(), includeResolved, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *Handler) RunCodexInspection(c *gin.Context) {
	service := h.getCodexInspectionService()
	if service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "codex inspection service unavailable"})
		return
	}
	detail, err := service.Run(c.Request.Context(), codexinspection.RunRequest{TriggerType: codexinspection.CodexInspectionTriggerManual})
	if err != nil {
		writeCodexInspectionError(c, err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

func (h *Handler) GetCodexInspectionRun(c *gin.Context) {
	service := h.getCodexInspectionService()
	if service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "codex inspection service unavailable"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid run id"})
		return
	}
	detail, err := service.GetRun(c.Request.Context(), id)
	if err != nil {
		writeCodexInspectionError(c, err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

func (h *Handler) ExecuteCodexInspectionActions(c *gin.Context) {
	service := h.getCodexInspectionService()
	if service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "codex inspection service unavailable"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid run id"})
		return
	}
	var req codexinspection.ExecuteActionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	result, err := service.ExecuteManualActions(c.Request.Context(), id, req)
	if err != nil {
		writeCodexInspectionError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func writeCodexInspectionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, codexinspection.ErrRunAlreadyActive):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, codexinspection.ErrNotConfigured):
		c.JSON(http.StatusPreconditionFailed, gin.H{"error": err.Error()})
	case errors.Is(err, codexinspection.ErrRunNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, codexinspection.ErrRunNotCompleted),
		errors.Is(err, codexinspection.ErrActionIDsRequired),
		errors.Is(err, codexinspection.ErrNoActionableResults):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
