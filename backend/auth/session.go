package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// SessionTTL is how long a login lasts without being used.
const SessionTTL = 30 * 24 * time.Hour

// dummyHash is compared against when there's no such account, so a login takes
// as long either way and doesn't say which usernames exist.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("no account has this password"), bcrypt.DefaultCost)

// Login checks a username and password and starts a session, returning its
// token. Every way of failing is ErrBadLogin.
func (s *Service) Login(username, password string) (string, Account, error) {
	_, key, err := NormalizeUsername(username)
	if err != nil {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return "", Account{}, ErrBadLogin
	}
	var id, hash string
	err = s.db.QueryRow(`select id, password_hash from accounts where username_key = $1`, key).Scan(&id, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return "", Account{}, ErrBadLogin
	}
	if err != nil {
		return "", Account{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return "", Account{}, ErrBadLogin
	}
	a, err := s.Get(id)
	if err != nil {
		return "", Account{}, err
	}
	if a.Disabled {
		return "", Account{}, ErrBadLogin
	}
	now := s.now()
	if _, err := s.db.Exec(`update accounts set last_login = $1 where id = $2`, now.UnixMilli(), id); err != nil {
		return "", Account{}, err
	}
	token, err := s.StartSession(id)
	return token, a, err
}

// StartSession starts a session for an account and returns its token. Only the
// token's hash is kept.
func (s *Service) StartSession(id string) (string, error) {
	b := make([]byte, 32)
	rand.Read(b)
	token := hex.EncodeToString(b)
	_, err := s.db.Exec(`insert into sessions (token_hash, account_id, expires_at) values ($1, $2, $3)`,
		hashSecret(token), id, s.now().Add(SessionTTL).UnixMilli())
	return token, err
}

// Authenticate is the account a session token belongs to, or ErrNoSession. A
// session past half its life is renewed.
func (s *Service) Authenticate(token string) (Account, error) {
	if token == "" {
		return Account{}, ErrNoSession
	}
	hash := hashSecret(token)
	var id string
	var expires int64
	err := s.db.QueryRow(`select account_id, expires_at from sessions where token_hash = $1`, hash).Scan(&id, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNoSession
	}
	if err != nil {
		return Account{}, err
	}
	now := s.now()
	if expires <= now.UnixMilli() {
		s.db.Exec(`delete from sessions where token_hash = $1`, hash)
		return Account{}, ErrNoSession
	}
	a, err := s.Get(id)
	if errors.Is(err, ErrNoAccount) || err == nil && a.Disabled {
		return Account{}, ErrNoSession
	}
	if err != nil {
		return Account{}, err
	}
	if expires-now.UnixMilli() < SessionTTL.Milliseconds()/2 {
		if _, err := s.db.Exec(`update sessions set expires_at = $1 where token_hash = $2`, now.Add(SessionTTL).UnixMilli(), hash); err != nil {
			return Account{}, err
		}
	}
	return a, nil
}

// Logout ends one session.
func (s *Service) Logout(token string) error {
	_, err := s.db.Exec(`delete from sessions where token_hash = $1`, hashSecret(token))
	return err
}

// RevokeAll ends all of an account's sessions.
func (s *Service) RevokeAll(id string) error {
	_, err := s.db.Exec(`delete from sessions where account_id = $1`, id)
	return err
}
