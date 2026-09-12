package database

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

var ErrUsernameTaken = errors.New("username already exists")

type User struct {
	ID          int64     `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Store) CreateUser(ctx context.Context, username, password, displayName string) (User, error) {
	username = strings.TrimSpace(username)
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = username
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	var user User
	err = s.pool.QueryRow(ctx, `
		INSERT INTO users(username,password_hash,display_name)
		VALUES($1,$2,$3)
		ON CONFLICT DO NOTHING
		RETURNING id,username,display_name,status,created_at`,
		username, string(hash), displayName).
		Scan(&user.ID, &user.Username, &user.DisplayName, &user.Status, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUsernameTaken
	}
	return user, err
}

func (s *Store) EnsureTestUser(ctx context.Context, username, password, displayName string) error {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil
	}
	if displayName = strings.TrimSpace(displayName); displayName == "" {
		displayName = "测试账号"
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO users(username,password_hash,display_name,status)
		VALUES($1,$2,$3,'active')
		ON CONFLICT ((LOWER(username))) DO UPDATE SET
			password_hash=EXCLUDED.password_hash,
			display_name=EXCLUDED.display_name,
			status='active'`, username, string(hash), displayName)
	return err
}
func (s *Store) AuthenticateUser(ctx context.Context, username, password string) (User, error) {
	var user User
	var hash string
	err := s.pool.QueryRow(ctx, `
		SELECT id,username,display_name,status,created_at,password_hash
		FROM users WHERE LOWER(username)=LOWER($1)`, strings.TrimSpace(username)).
		Scan(&user.ID, &user.Username, &user.DisplayName, &user.Status, &user.CreatedAt, &hash)
	if err != nil {
		return User{}, err
	}
	if user.Status != "active" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return User{}, pgx.ErrNoRows
	}
	return user, nil
}

func (s *Store) GetUser(ctx context.Context, id int64) (User, error) {
	var user User
	err := s.pool.QueryRow(ctx,
		`SELECT id,username,display_name,status,created_at FROM users WHERE id=$1`, id).
		Scan(&user.ID, &user.Username, &user.DisplayName, &user.Status, &user.CreatedAt)
	return user, err
}
