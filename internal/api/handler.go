package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"matchmind/internal/auth"
	"matchmind/internal/database"
)

type Handler struct {
	store  *database.Store
	origin string
	tokens *auth.Manager
}

func NewHandler(store *database.Store, origin string, tokens *auth.Manager) *Handler {
	return &Handler{store: store, origin: origin, tokens: tokens}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /api/v1/auth/register", h.register)
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	protected := http.NewServeMux()
	protected.HandleFunc("GET /api/v1/auth/me", h.me)
	protected.HandleFunc("POST /api/v1/ingestion-batches", h.createBatch)
	protected.HandleFunc("GET /api/v1/ingestion-batches", h.listBatches)
	protected.HandleFunc("GET /api/v1/ingestion-batches/{id}", h.getBatch)
	protected.HandleFunc("POST /api/v1/ingestion-batches/{id}/retry", h.retryBatch)
	protected.HandleFunc("GET /api/v1/buy-demands", h.listBuyDemands)
	protected.HandleFunc("GET /api/v1/sell-projects", h.listSellProjects)
	protected.HandleFunc("GET /api/v1/entities/{type}/{id}", h.getEntity)
	protected.HandleFunc("GET /api/v1/entities/{type}/{id}/matches", h.getMatches)
	protected.HandleFunc("GET /api/v1/duplicate-candidates", h.listDuplicateCandidates)
	protected.HandleFunc("POST /api/v1/duplicate-candidates/{id}/merge", h.mergeDuplicate)
	protected.HandleFunc("POST /api/v1/duplicate-candidates/{id}/reject", h.rejectDuplicate)
	protected.HandleFunc("POST /api/v1/merge-events/{id}/revert", h.revertMerge)
	protected.HandleFunc("POST /api/v1/embeddings/backfill", h.backfillEmbeddings)
	protected.HandleFunc("POST /api/v1/matches/rebuild", h.rebuildMatches)
	mux.Handle("/api/v1/", h.requireAuth(protected))
	return h.middleware(h.recoverPanic(mux))
}

func (h *Handler) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		r = r.WithContext(context.WithValue(r.Context(), requestIDContextKey{}, id))
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("Access-Control-Allow-Origin", h.origin)
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) createBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content         string `json:"content"`
		SourceName      string `json:"source_name"`
		SourceFile      string `json:"source_file"`
		ClientRequestID string `json:"client_request_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求格式错误")
		return
	}
	req.Content = strings.TrimSpace(req.Content)
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content_required", "请输入投资需求或项目信息")
		return
	}
	if len(strings.TrimSpace(req.ClientRequestID)) > 100 {
		writeError(w, http.StatusBadRequest, "invalid_client_request_id", "提交标识无效")
		return
	}
	id, status, err := h.store.CreateBatch(r.Context(), req.Content, req.SourceName, req.SourceFile, req.ClientRequestID, userID(r))
	if err != nil {
		h.internalError(w, r, "database", err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"batch_id": id, "status": status})
}

func (h *Handler) listBatches(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	items, total, err := h.store.ListBatches(r.Context(), userID(r), pageSize, (page-1)*pageSize)
	if err != nil {
		h.internalError(w, r, "database", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "page_size": pageSize})
}

func (h *Handler) getBatch(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, 400, "invalid_id", "无效批次ID")
		return
	}
	batch, err := h.store.GetBatch(r.Context(), id, userID(r))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "批次不存在")
		return
	}
	if err != nil {
		h.internalError(w, r, "database", err)
		return
	}
	writeJSON(w, 200, batch)
}

func (h *Handler) retryBatch(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, 400, "invalid_id", "无效批次ID")
		return
	}
	if err := h.store.RetryBatch(r.Context(), id, userID(r)); errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "批次不存在或当前状态不可重试")
		return
	} else if err != nil {
		h.internalError(w, r, "database", err)
		return
	}
	writeJSON(w, 202, map[string]any{"batch_id": id, "status": "retry"})
}

func (h *Handler) listBuyDemands(w http.ResponseWriter, r *http.Request) {
	h.listEntities(w, r, "buy_demand")
}
func (h *Handler) listSellProjects(w http.ResponseWriter, r *http.Request) {
	h.listEntities(w, r, "sell_project")
}

func (h *Handler) listEntities(w http.ResponseWriter, r *http.Request, entityType string) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	items, total, err := h.store.ListEntities(r.Context(), entityType, r.URL.Query().Get("q"), pageSize, (page-1)*pageSize)
	if err != nil {
		h.internalError(w, r, "database", err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total, "page": page, "page_size": pageSize})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
