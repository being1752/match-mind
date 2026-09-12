package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

type requestIDContextKey struct{}

func newRequestID() string {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err == nil {
		return "req_" + hex.EncodeToString(value)
	}
	return "req_unknown"
}

func requestID(r *http.Request) string {
	value, _ := r.Context().Value(requestIDContextKey{}).(string)
	return value
}

func (h *Handler) internalError(w http.ResponseWriter, r *http.Request, component string, err error) {
	status, code, message := http.StatusInternalServerError, "internal_error", "系统处理失败，请稍后重试"
	if errors.Is(err, context.DeadlineExceeded) {
		status, code, message = http.StatusGatewayTimeout, "request_timeout", "请求处理超时，请稍后重试"
	} else if component == "database" && isDatabaseUnavailable(err) {
		status, code, message = http.StatusServiceUnavailable, "database_unavailable", "数据服务暂时不可用，请稍后重试"
	}
	id := requestID(r)
	slog.Error("request failed", "request_id", id, "component", component, "method", r.Method, "path", r.URL.Path, "user_id", userID(r), "error", err)
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": id}})
}

func isDatabaseUnavailable(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return strings.HasPrefix(pgErr.Code, "08") || strings.HasPrefix(pgErr.Code, "53") || pgErr.Code == "57P01"
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "failed to connect") || strings.Contains(text, "connection refused") ||
		strings.Contains(text, "connection reset") || strings.Contains(text, "broken pipe") || strings.Contains(text, "unexpected eof")
}

func (h *Handler) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				id := requestID(r)
				slog.Error("request panic", "request_id", id, "method", r.Method, "path", r.URL.Path, "panic", recovered, "stack", string(debug.Stack()))
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]string{"code": "internal_error", "message": "系统处理失败，请稍后重试", "request_id": id}})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
