package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mosterakie/DevLens/internal/domain"
	"github.com/mosterakie/DevLens/internal/repository"
	"github.com/mosterakie/DevLens/internal/service"
)

// IncidentHandler 暴露 Incident 相关的接口。
type IncidentHandler struct {
	svc   *service.IncidentService
	arepo *repository.AnalysisRepo
}

// NewIncidentHandler 构造 handler。
func NewIncidentHandler(svc *service.IncidentService, arepo *repository.AnalysisRepo) *IncidentHandler {
	return &IncidentHandler{svc: svc, arepo: arepo}
}

// --- 请求与响应结构 ---

type analyzeRequest struct {
	Log string `json:"log"`
}

type relatedIncidentDTO struct {
	ID        int64         `json:"id"`
	Title     string        `json:"title"`
	Severity  *string       `json:"severity"`
	Status    domain.Status `json:"status"`
	CreatedAt time.Time     `json:"created_at"`
}

type analyzeResponse struct {
	ID                 int64                `json:"id"`
	Status             domain.Status        `json:"status"`
	IsRecurring        bool                 `json:"is_recurring"`
	Lang               string               `json:"lang"`
	RelatedIncidentIDs []int64              `json:"related_incident_ids"`
	RelatedIncidents   []relatedIncidentDTO `json:"related_incidents"`
	CreatedAt          time.Time            `json:"created_at"`
}

type evidenceDTO struct {
	Key        string `json:"key"`
	Value      string `json:"value"`
	SourceLine int    `json:"source_line"`
}

type analysisDTO struct {
	Summary          string        `json:"summary"`
	PossibleCauses   []string      `json:"possible_causes"`
	Evidence         []evidenceDTO `json:"evidence"`
	SuggestedActions []string      `json:"suggested_actions"`
	Confidence       float64       `json:"confidence"`
	Model            string        `json:"model"`
	PromptVersion    string        `json:"prompt_version"`
}

type incidentDTO struct {
	ID          int64         `json:"id"`
	Title       string        `json:"title"`
	RawLog      string        `json:"raw_log"`
	Status      domain.Status `json:"status"`
	Severity    *string       `json:"severity"`
	Category    string        `json:"category,omitempty"`
	IsRecurring bool          `json:"is_recurring"`
	Lang        string        `json:"lang,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	Analysis    *analysisDTO  `json:"analysis"`
}

type listResponse struct {
	Items      []incidentDTO `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

type eventDTO struct {
	EventType  string         `json:"event_type"`
	FromStatus *domain.Status `json:"from_status,omitempty"`
	ToStatus   *domain.Status `json:"to_status,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

type eventsResponse struct {
	Items []eventDTO `json:"items"`
}

type relatedResponse struct {
	Items []relatedIncidentDTO `json:"items"`
}

type statusRequest struct {
	Status domain.Status `json:"status"`
}

// --- Handlers ---

// Analyze 处理 POST /api/v1/incidents/analyze。
//
// 返回 202 而不是 200：记录已接受，但 AI 诊断还在后台进行。
// fingerprint 匹配是同步的，所以 related 字段此时已有值。
func (h *IncidentHandler) Analyze(c *gin.Context) {
	var req analyzeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, CodeInvalidRequest, "invalid JSON body")
		return
	}

	res, err := h.svc.SubmitLang(c.Request.Context(), req.Log, parseLang(c))
	if err != nil {
		// 入队失败时 Incident 已经建好，仍然返回它，让用户能查询状态。
		// 携带错误的情况由 worker 的兜底扫描处理。
		if res == nil || res.Incident == nil {
			failFromError(c, err)
			return
		}
	}

	ids := make([]int64, 0, len(res.Related))
	related := make([]relatedIncidentDTO, 0, len(res.Related))
	for _, r := range res.Related {
		ids = append(ids, r.ID)
		related = append(related, relatedIncidentDTO{
			ID:        r.ID,
			Title:     r.Title,
			Severity:  severityPtrFromPtr(r.Severity),
			Status:    r.Status,
			CreatedAt: r.CreatedAt,
		})
	}

	c.JSON(http.StatusAccepted, analyzeResponse{
		ID:                 res.Incident.ID,
		Status:             res.Incident.Status,
		IsRecurring:        res.Recurring,
		Lang:               string(res.Incident.Lang),
		RelatedIncidentIDs: ids,
		RelatedIncidents:   related,
		CreatedAt:          res.Incident.CreatedAt,
	})
}

// Get 处理 GET /api/v1/incidents/:id。
//
// 分析未完成时返回 200 且 analysis 为 null，而不是 425。
// 前端轮询时就不用为"还没好"单独写异常分支。
func (h *IncidentHandler) Get(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	ctx := c.Request.Context()
	inc, err := h.svc.Get(ctx, id)
	if err != nil {
		failFromError(c, err)
		return
	}

	dto := toIncidentDTO(inc)

	a, err := h.arepo.GetByIncidentID(ctx, id)
	switch {
	case err == nil:
		dto.Analysis = toAnalysisDTO(a)
	case errors.Is(err, repository.ErrNotFound):
		// 正常情况：分析尚未完成。
	default:
		failFromError(c, err)
		return
	}

	c.JSON(http.StatusOK, dto)
}

// List 处理 GET /api/v1/incidents。
func (h *IncidentHandler) List(c *gin.Context) {
	ctx := c.Request.Context()

	f := repository.ListFilter{Limit: 20}
	if v := c.Query("status"); v != "" {
		s := domain.Status(v)
		if !s.IsValid() {
			fail(c, http.StatusBadRequest, CodeInvalidRequest, "invalid status filter")
			return
		}
		f.Status = &s
	}
	if v := c.Query("severity"); v != "" {
		s := domain.Severity(v)
		if !s.IsValid() {
			fail(c, http.StatusBadRequest, CodeInvalidRequest, "invalid severity filter")
			return
		}
		f.Severity = &s
	}
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > 100 {
			fail(c, http.StatusBadRequest, CodeInvalidRequest, "limit must be between 1 and 100")
			return
		}
		f.Limit = n
	}

	items, err := h.svc.List(ctx, f)
	if err != nil {
		failFromError(c, err)
		return
	}

	out := make([]incidentDTO, 0, len(items))
	for _, inc := range items {
		out = append(out, toIncidentDTO(inc))
	}

	resp := listResponse{Items: out}
	// 取最后一条作为下一页游标。
	if len(items) == f.Limit {
		last := items[len(items)-1]
		resp.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	}

	c.JSON(http.StatusOK, resp)
}

// Related 处理 GET /api/v1/incidents/:id/related。
//
// 同类问题在提交时已经算过一次，这个端点让详情页也能拿到，
// 而不必依赖提交时的响应。
func (h *IncidentHandler) Related(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	items, err := h.svc.RelatedByIncidentID(c.Request.Context(), id)
	if err != nil {
		failFromError(c, err)
		return
	}

	out := make([]relatedIncidentDTO, 0, len(items))
	for _, r := range items {
		out = append(out, relatedIncidentDTO{
			ID:        r.ID,
			Title:     r.Title,
			Severity:  severityPtrFromPtr(r.Severity),
			Status:    r.Status,
			CreatedAt: r.CreatedAt,
		})
	}

	c.JSON(http.StatusOK, relatedResponse{Items: out})
}

// Events 处理 GET /api/v1/incidents/:id/events。
func (h *IncidentHandler) Events(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	events, err := h.svc.Events(c.Request.Context(), id)
	if err != nil {
		failFromError(c, err)
		return
	}

	out := make([]eventDTO, 0, len(events))
	for _, e := range events {
		out = append(out, eventDTO{
			EventType:  e.EventType,
			FromStatus: e.FromStatus,
			ToStatus:   e.ToStatus,
			CreatedAt:  e.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, eventsResponse{Items: out})
}

// ChangeStatus 处理 PATCH /api/v1/incidents/:id/status。
//
// 做成子资源而不是 PATCH /incidents/:id，是因为状态转移有副作用
// （校验合法性、写事件），和改标题这类幂等更新不是一回事。
func (h *IncidentHandler) ChangeStatus(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	var req statusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, CodeInvalidRequest, "invalid JSON body")
		return
	}

	inc, err := h.svc.ChangeStatus(c.Request.Context(), id, req.Status)
	if err != nil {
		failFromError(c, err)
		return
	}
	c.JSON(http.StatusOK, toIncidentDTO(inc))
}

// --- 辅助函数 ---

func parseID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		fail(c, http.StatusBadRequest, CodeInvalidRequest, "invalid incident id")
		return 0, false
	}
	return id, true
}

// severityPtr 把可空的严重程度转成可空字符串。
// 空字符串表示尚未分析出结果，序列化成 null。
func severityPtr(s domain.Severity) *string {
	if s == "" {
		return nil
	}
	v := string(s)
	return &v
}

// severityPtrFromPtr 处理 RelatedIncident 里的可空严重程度。
func severityPtrFromPtr(s *domain.Severity) *string {
	if s == nil {
		return nil
	}
	return severityPtr(*s)
}

func toIncidentDTO(inc *domain.Incident) incidentDTO {
	return incidentDTO{
		ID:          inc.ID,
		Title:       inc.Title,
		RawLog:      inc.RawLog,
		Status:      inc.Status,
		Severity:    severityPtr(inc.Severity),
		Category:    inc.Category,
		IsRecurring: inc.IsRecurring,
		Lang:        string(inc.Lang),
		CreatedAt:   inc.CreatedAt,
		UpdatedAt:   inc.UpdatedAt,
	}
}

func toAnalysisDTO(a *domain.Analysis) *analysisDTO {
	ev := make([]evidenceDTO, 0, len(a.Evidence))
	for _, e := range a.Evidence {
		ev = append(ev, evidenceDTO{Key: e.Key, Value: e.Value, SourceLine: e.SourceLine})
	}
	causes := a.PossibleCauses
	if causes == nil {
		causes = []string{}
	}
	actions := a.SuggestedActions
	if actions == nil {
		actions = []string{}
	}
	return &analysisDTO{
		Summary:          a.Summary,
		PossibleCauses:   causes,
		Evidence:         ev,
		SuggestedActions: actions,
		Confidence:       a.Confidence,
		Model:            a.Model,
		PromptVersion:    a.PromptVersion,
	}
}
