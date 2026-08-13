package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type Claims struct {
	UserID   int64  `json:"uid"`
	Username string `json:"username"`
	Expires  int64  `json:"exp"`
}

type Manager struct {
	secret []byte
	ttl    time.Duration
}

func NewManager(secret string, ttl time.Duration) *Manager {
	return &Manager{secret: []byte(secret), ttl: ttl}
}

func (m *Manager) Issue(id int64, username string) (string, error) {
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	body, err := json.Marshal(Claims{id, username, time.Now().Add(m.ttl).Unix()})
	if err != nil {
		return "", err
	}
	unsigned := enc(header) + "." + enc(body)
	return unsigned + "." + m.sign(unsigned), nil
}

func (m *Manager) Parse(value string) (Claims, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return Claims{}, errors.New("invalid token")
	}
	expected := m.sign(parts[0] + "." + parts[1])
	if !hmac.Equal([]byte(parts[2]), []byte(expected)) {
		return Claims{}, errors.New("invalid token")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, errors.New("invalid token")
	}
	var claims Claims
	if json.Unmarshal(body, &claims) != nil || claims.UserID == 0 {
		return Claims{}, errors.New("invalid token")
	}
	if time.Now().After(time.Unix(claims.Expires, 0)) {
		return Claims{}, errors.New("expired token")
	}
	return claims, nil
}

func (m *Manager) sign(value string) string {
	h := hmac.New(sha256.New, m.secret)
	_, _ = h.Write([]byte(value))
	return enc(h.Sum(nil))
}

func enc(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}

func Bearer(header string) (string, error) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", errors.New("bearer token required")
	}
	return parts[1], nil
}
