package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
)

func entityParams(r *http.Request) (string, int64, error) {
	entityType := r.PathValue("type")
	if entityType != "buy_demand" && entityType != "sell_project" {
		return "", 0, errors.New("invalid entity type")
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return entityType, id, err
}

func (h *Handler) getEntity(w http.ResponseWriter, r *http.Request) {
	entityType, id, err := entityParams(r)
	if err != nil {
		writeError(w, 400, "invalid_entity", "实体参数错误")
		return
	}
	result, err := h.store.GetEntityDetail(r.Context(), entityType, id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "信息不存在")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	writeJSON(w, 200, result)
}

func (h *Handler) getMatches(w http.ResponseWriter, r *http.Request) {
	entityType, id, err := entityParams(r)
	if err != nil {
		writeError(w, 400, "invalid_entity", "实体参数错误")
		return
	}
	items, err := h.store.ListMatches(r.Context(), entityType, id)
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (h *Handler) listDuplicateCandidates(w http.ResponseWriter, r *http.Request) {
	items, err := h.store.ListDuplicateCandidates(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (h *Handler) mergeDuplicate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, 400, "invalid_id", "候选ID错误")
		return
	}
	if err := h.store.MergeEntities(r.Context(), id, userID(r)); err != nil {
		writeError(w, 500, "merge_failed", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"status": "merged"})
}

func (h *Handler) rejectDuplicate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, 400, "invalid_id", "候选ID错误")
		return
	}
	if err := h.store.RejectDuplicate(r.Context(), id, userID(r)); err != nil {
		writeError(w, 500, "reject_failed", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"status": "rejected"})
}

func (h *Handler) backfillEmbeddings(w http.ResponseWriter, r *http.Request) {
	if err := h.store.QueueMissingEmbeddings(r.Context()); err != nil {
		writeError(w, 500, "queue_failed", err.Error())
		return
	}
	writeJSON(w, 202, map[string]any{"status": "queued"})
}

func (h *Handler) rebuildMatches(w http.ResponseWriter, r *http.Request) {
	count, err := h.store.RebuildAllMatches(r.Context())
	if err != nil {
		writeError(w, 500, "rebuild_failed", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"status": "completed", "sources_processed": count})
}

func (h *Handler) revertMerge(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, 400, "invalid_id", "合并事件ID错误")
		return
	}
	if err := h.store.RevertMerge(r.Context(), id, userID(r)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "not_found", "可撤销的合并事件不存在")
			return
		}
		writeError(w, 500, "revert_failed", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"status": "reverted"})
}
