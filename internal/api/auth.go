package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"matchmind/internal/auth"
	"matchmind/internal/database"
)

type userContextKey struct{}

func userID(r *http.Request) int64 {
	value, _ := r.Context().Value(userContextKey{}).(int64)
	return value
}

func (h *Handler) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value, err := auth.Bearer(r.Header.Get("Authorization"))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		claims, err := h.tokens.Parse(value)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "登录已过期，请重新登录")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey{}, claims.UserID)))
	})
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req) != nil {
		writeError(w, 400, "invalid_json", "请求格式错误")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) < 3 || len(req.Username) > 80 {
		writeError(w, 400, "invalid_username", "用户名长度需要在3到80个字符之间")
		return
	}
	if len(req.Password) < 8 || len(req.Password) > 128 {
		writeError(w, 400, "invalid_password", "密码长度需要在8到128个字符之间")
		return
	}
	user, err := h.store.CreateUser(r.Context(), req.Username, req.Password, req.DisplayName)
	if errors.Is(err, database.ErrUsernameTaken) {
		writeError(w, 409, "username_taken", "用户名已存在")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	h.writeSession(w, user)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req) != nil {
		writeError(w, 400, "invalid_json", "请求格式错误")
		return
	}
	user, err := h.store.AuthenticateUser(r.Context(), req.Username, req.Password)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 401, "invalid_credentials", "用户名或密码错误")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	h.writeSession(w, user)
}

func (h *Handler) writeSession(w http.ResponseWriter, user database.User) {
	token, err := h.tokens.Issue(user.ID, user.Username)
	if err != nil {
		writeError(w, 500, "token_error", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"token": token, "user": user})
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	user, err := h.store.GetUser(r.Context(), userID(r))
	if err != nil {
		writeError(w, 404, "not_found", "用户不存在")
		return
	}
	writeJSON(w, 200, user)
}
