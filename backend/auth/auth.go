// Package auth keeps who may use the site: accounts, their roles, the invite
// codes they registered with, and their sessions. It knows nothing of poker.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// Role is what an account may do.
type Role string

const (
	RoleRoot  Role = "root"  // manages every account, one per site
	RoleAdmin Role = "admin" // creates tables
	RoleUser  Role = "user"  // joins tables
)

const (
	DefaultInviteTTL = 7 * 24 * time.Hour
	minPasswordLen   = 8
	maxPasswordLen   = 72 // bcrypt reads no further, in bytes
)

var (
	ErrUsernameTaken = errors.New("用户名已被占用")
	ErrBadUsername   = errors.New("用户名需为 2 到 20 个字符，只能包含文字、数字、下划线和连字符")
	ErrBadPassword   = errors.New("密码需为 8 到 72 个字符")
	ErrBadInvite     = errors.New("邀请码无效")
	ErrBadLogin      = errors.New("用户名或密码错误")
	ErrRootProtected = errors.New("root 账号不能被禁用或降级，也不能新增 root")
	ErrBadRole       = errors.New("无效的角色")
	ErrNoAccount     = errors.New("账号不存在")
	ErrNoSession     = errors.New("未登录或登录已过期")
	ErrNoInvite      = errors.New("邀请码不存在或已被使用")
)

// Account is someone who can log in. ID never changes; Username can.
type Account struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	Role       Role   `json:"role"`
	Disabled   bool   `json:"disabled"`
	MustChange bool   `json:"mustChange"` // has to set a new password first
	CreatedAt  int64  `json:"createdAt"`
	LastLogin  int64  `json:"lastLogin"`
}

// CanCreateTables says whether a may start a table.
func (a Account) CanCreateTables() bool {
	return a.Role == RoleRoot || a.Role == RoleAdmin
}

// Invite is a code as root sees it. The code itself is only shown when made.
type Invite struct {
	ID        string `json:"id"`
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt"`
	RevokedAt int64  `json:"revokedAt"`
	UsedBy    string `json:"usedBy"` // username of who registered with it now
	UsedAt    int64  `json:"usedAt"`
}

// Service keeps accounts in the site's database.
type Service struct {
	db   *sql.DB
	cost int
	now  func() time.Time
}

func New(db *sql.DB) *Service {
	return &Service{db: db, cost: bcrypt.DefaultCost, now: time.Now}
}

const accountColumns = `id, username, role, disabled, must_change, created_at, last_login`

func scanAccount(row interface{ Scan(...any) error }) (Account, error) {
	var a Account
	var disabled, mustChange int
	err := row.Scan(&a.ID, &a.Username, &a.Role, &disabled, &mustChange, &a.CreatedAt, &a.LastLogin)
	a.Disabled, a.MustChange = disabled != 0, mustChange != 0
	return a, err
}

func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// randomCode is at least 80 bits of randomness that's easy to read out.
func randomCode() string {
	b := make([]byte, 10)
	rand.Read(b)
	return base32.StdEncoding.EncodeToString(b)
}

func (s *Service) hashPassword(password string) (string, error) {
	if n := len([]rune(password)); n < minPasswordLen || len(password) > maxPasswordLen {
		return "", ErrBadPassword
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), s.cost)
	return string(h), err
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}

// EnsureRoot creates root if the site has none. Once there is one, it does
// nothing, whatever it's given.
func (s *Service) EnsureRoot(username, password string) error {
	var n int
	if err := s.db.QueryRow(`select count(*) from accounts where role = $1`, RoleRoot).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	display, key, err := NormalizeUsername(username)
	if err != nil {
		return err
	}
	hash, err := s.hashPassword(password)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`insert into accounts (id, username, username_key, password_hash, role, must_change, created_at)
		values ($1, $2, $3, $4, $5, 1, $6)`, uuid.NewString(), display, key, hash, RoleRoot, s.now().UnixMilli())
	return err
}

// CreateInvite makes a code that registers one account, and returns it. Only
// its hash is kept, so this is the one time it can be seen.
func (s *Service) CreateInvite(by string, ttl time.Duration) (string, error) {
	code := randomCode()
	now := s.now()
	_, err := s.db.Exec(`insert into invites (id, code_hash, created_by, created_at, expires_at) values ($1, $2, $3, $4, $5)`,
		uuid.NewString(), hashSecret(code), by, now.UnixMilli(), now.Add(ttl).UnixMilli())
	if err != nil {
		return "", err
	}
	return code, nil
}

// ListInvites is every invite, newest first.
func (s *Service) ListInvites() ([]Invite, error) {
	rows, err := s.db.Query(`select i.id, i.created_at, i.expires_at, i.revoked_at, coalesce(a.username, ''), i.used_at
		from invites i left join accounts a on a.id = i.used_by order by i.created_at desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Invite{}
	for rows.Next() {
		var i Invite
		if err := rows.Scan(&i.ID, &i.CreatedAt, &i.ExpiresAt, &i.RevokedAt, &i.UsedBy, &i.UsedAt); err != nil {
			return nil, err
		}
		list = append(list, i)
	}
	return list, rows.Err()
}

// RevokeInvite stops an unused code from working.
func (s *Service) RevokeInvite(id string) error {
	res, err := s.db.Exec(`update invites set revoked_at = $1 where id = $2 and used_by = '' and revoked_at = 0`, s.now().UnixMilli(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoInvite
	}
	return nil
}

// Register creates a user with an invite code. The code is spent only if the
// account is created, and only one registration can ever spend it.
func (s *Service) Register(username, password, code string) (Account, error) {
	display, key, err := NormalizeUsername(username)
	if err != nil {
		return Account{}, err
	}
	hash, err := s.hashPassword(password)
	if err != nil {
		return Account{}, err
	}
	code = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(code))
	now := s.now().UnixMilli()
	a := Account{ID: uuid.NewString(), Username: display, Role: RoleUser, CreatedAt: now}

	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`update invites set used_by = $1, used_at = $2
		where code_hash = $3 and used_by = '' and revoked_at = 0 and expires_at > $4`, a.ID, now, hashSecret(code), now)
	if err != nil {
		return Account{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Account{}, ErrBadInvite
	}
	_, err = tx.Exec(`insert into accounts (id, username, username_key, password_hash, role, created_at)
		values ($1, $2, $3, $4, $5, $6)`, a.ID, display, key, hash, RoleUser, now)
	if isUniqueViolation(err) {
		return Account{}, ErrUsernameTaken
	}
	if err != nil {
		return Account{}, err
	}
	return a, tx.Commit()
}

// Get is the account with id.
func (s *Service) Get(id string) (Account, error) {
	a, err := scanAccount(s.db.QueryRow(`select `+accountColumns+` from accounts where id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNoAccount
	}
	return a, err
}

// ListAccounts is every account, oldest first.
func (s *Service) ListAccounts() ([]Account, error) {
	rows, err := s.db.Query(`select ` + accountColumns + ` from accounts order by created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Account{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

// UsernameTaken says whether an account has name, whatever its case.
func (s *Service) UsernameTaken(name string) (bool, error) {
	_, key, err := NormalizeUsername(name)
	if err != nil {
		return false, nil // no account can have a name that isn't valid
	}
	var n int
	err = s.db.QueryRow(`select count(*) from accounts where username_key = $1`, key).Scan(&n)
	return n > 0, err
}

// Rename changes an account's username. Its old name is free straight away.
func (s *Service) Rename(id, username string) error {
	display, key, err := NormalizeUsername(username)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`update accounts set username = $1, username_key = $2 where id = $3`, display, key, id)
	if isUniqueViolation(err) {
		return ErrUsernameTaken
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoAccount
	}
	return nil
}

// ChangePassword sets a new password and ends all the account's sessions.
func (s *Service) ChangePassword(id, password string) error {
	hash, err := s.hashPassword(password)
	if err != nil {
		return err
	}
	return s.setPassword(id, hash, false)
}

func (s *Service) setPassword(id, hash string, mustChange bool) error {
	flag := 0
	if mustChange {
		flag = 1
	}
	res, err := s.db.Exec(`update accounts set password_hash = $1, must_change = $2 where id = $3`, hash, flag, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoAccount
	}
	return s.RevokeAll(id)
}

// CheckPassword says whether password is the account's.
func (s *Service) CheckPassword(id, password string) bool {
	var hash string
	if err := s.db.QueryRow(`select password_hash from accounts where id = $1`, id).Scan(&hash); err != nil {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// ResetPassword gives an account a temporary password, returned this once,
// that has to be changed at the next login.
func (s *Service) ResetPassword(id string) (string, error) {
	temp := randomCode()
	hash, err := s.hashPassword(temp)
	if err != nil {
		return "", err
	}
	return temp, s.setPassword(id, hash, true)
}

// ResetRoot does ResetPassword for root, for when its password is lost.
func (s *Service) ResetRoot() (username, password string, err error) {
	var id string
	err = s.db.QueryRow(`select id, username from accounts where role = $1`, RoleRoot).Scan(&id, &username)
	if err != nil {
		return "", "", err
	}
	password, err = s.ResetPassword(id)
	return username, password, err
}

// SetRole makes an account an admin or a user. Root stays as it is, and there
// can't be another.
func (s *Service) SetRole(id string, role Role) error {
	if role == RoleRoot {
		return ErrRootProtected
	}
	if role != RoleAdmin && role != RoleUser {
		return ErrBadRole
	}
	a, err := s.Get(id)
	if err != nil {
		return err
	}
	if a.Role == RoleRoot {
		return ErrRootProtected
	}
	_, err = s.db.Exec(`update accounts set role = $1 where id = $2`, role, id)
	return err
}

// SetDisabled stops an account logging in and ends its sessions, or lets it
// back. Root can't be disabled.
func (s *Service) SetDisabled(id string, disabled bool) error {
	a, err := s.Get(id)
	if err != nil {
		return err
	}
	if a.Role == RoleRoot {
		return ErrRootProtected
	}
	flag := 0
	if disabled {
		flag = 1
	}
	if _, err := s.db.Exec(`update accounts set disabled = $1 where id = $2`, flag, id); err != nil {
		return err
	}
	if disabled {
		return s.RevokeAll(id)
	}
	return nil
}
