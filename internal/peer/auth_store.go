package peer

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	_ "github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

type AuthStore struct {
	db         *sql.DB
	sessionTTL time.Duration
}

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

func NewAuthStore(databaseURL string, sessionTTL time.Duration) (*AuthStore, error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	store := &AuthStore{db: db, sessionTTL: sessionTTL}
	if err := store.migrate(context.Background()); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *AuthStore) migrate(ctx context.Context) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id BIGSERIAL PRIMARY KEY,
			username TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token TEXT PRIMARY KEY,
			user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			expires_at TIMESTAMPTZ NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,
		`CREATE TABLE IF NOT EXISTS file_actions (
			id BIGSERIAL PRIMARY KEY,
			user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			action TEXT NOT NULL,
			file_id TEXT NOT NULL,
			file_name TEXT NOT NULL,
			size_bytes BIGINT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,
	}
	for _, q := range queries {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

func (s *AuthStore) RegisterUser(ctx context.Context, username, password string) (User, error) {
	if username == "" || password == "" {
		return User{}, errors.New("username and password are required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	var out User
	err = s.db.QueryRowContext(
		ctx,
		`INSERT INTO users (username, password_hash) VALUES ($1, $2) RETURNING id, username`,
		username,
		string(hash),
	).Scan(&out.ID, &out.Username)
	if err != nil {
		return User{}, err
	}
	return out, nil
}

func (s *AuthStore) Login(ctx context.Context, username, password string) (string, User, error) {
	var (
		userID       int64
		passwordHash string
	)
	err := s.db.QueryRowContext(
		ctx,
		`SELECT id, username, password_hash FROM users WHERE username = $1`,
		username,
	).Scan(&userID, &username, &passwordHash)
	if err != nil {
		return "", User{}, errors.New("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		return "", User{}, errors.New("invalid credentials")
	}
	token, err := newToken()
	if err != nil {
		return "", User{}, err
	}
	_, err = s.db.ExecContext(
		ctx,
		`INSERT INTO sessions (token, user_id, expires_at) VALUES ($1, $2, $3)`,
		token,
		userID,
		time.Now().Add(s.sessionTTL),
	)
	if err != nil {
		return "", User{}, err
	}
	return token, User{ID: userID, Username: username}, nil
}

func (s *AuthStore) UserByToken(ctx context.Context, token string) (User, error) {
	var user User
	err := s.db.QueryRowContext(
		ctx,
		`SELECT u.id, u.username
		 FROM sessions s
		 JOIN users u ON u.id = s.user_id
		 WHERE s.token = $1 AND s.expires_at > NOW()`,
		token,
	).Scan(&user.ID, &user.Username)
	if err != nil {
		return User{}, errors.New("unauthorized")
	}
	return user, nil
}

func (s *AuthStore) Logout(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token = $1`, token)
	return err
}

func (s *AuthStore) AddAction(ctx context.Context, userID int64, action, fileID, fileName string, size int64) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO file_actions (user_id, action, file_id, file_name, size_bytes) VALUES ($1, $2, $3, $4, $5)`,
		userID,
		action,
		fileID,
		fileName,
		size,
	)
	return err
}

type FileAction struct {
	Username  string `json:"username"`
	Action    string `json:"action"`
	FileID    string `json:"file_id"`
	FileName  string `json:"file_name"`
	SizeBytes int64  `json:"size_bytes"`
	CreatedAt string `json:"created_at"`
}

func (s *AuthStore) ListActions(ctx context.Context, userID int64) ([]FileAction, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT u.username, file_actions.action, file_actions.file_id, file_actions.file_name, file_actions.size_bytes, file_actions.created_at::text
		 FROM file_actions
		 JOIN users u ON u.id = file_actions.user_id
		 WHERE file_actions.user_id = $1
		 ORDER BY file_actions.created_at DESC
		 LIMIT 50`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]FileAction, 0)
	for rows.Next() {
		var item FileAction
		if err := rows.Scan(&item.Username, &item.Action, &item.FileID, &item.FileName, &item.SizeBytes, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *AuthStore) ListAllActions(ctx context.Context) ([]FileAction, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT u.username, fa.action, fa.file_id, fa.file_name, fa.size_bytes, fa.created_at::text
		 FROM file_actions fa
		 JOIN users u ON u.id = fa.user_id
		 ORDER BY fa.created_at DESC
		 LIMIT 100`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]FileAction, 0)
	for rows.Next() {
		var item FileAction
		if err := rows.Scan(&item.Username, &item.Action, &item.FileID, &item.FileName, &item.SizeBytes, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
