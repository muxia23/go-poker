package server

import (
	_ "embed"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/evanofslack/go-poker/auth"
	"github.com/go-chi/chi/v5"
)

const (
	// basePath is where the whole site lives.
	basePath      = "/poker"
	sessionCookie = "poker_session"
	loginWindow   = 15 * time.Minute
	maxAuthBody   = 4096
)

// loginAttempts is how many failures an address or name gets per loginWindow.
var loginAttempts = 5

//go:embed login.html
var loginPage string

// mountAuth adds the login page and the account APIs.
func (s *Server) mountAuth() {
	s.router.Get(basePath+"/login", s.serveLogin)
	s.router.Route(basePath+"/api", func(r chi.Router) {
		r.Use(sameOriginPosts)
		r.Post("/auth/login", s.login)
		r.Post("/auth/register", s.register)
		r.Post("/auth/logout", s.loggedIn(true, s.logout))
		r.Get("/auth/me", s.loggedIn(true, s.me))
		r.Post("/auth/password", s.loggedIn(true, s.changePassword))
		r.Post("/auth/username", s.loggedIn(false, s.changeUsername))
		r.Get("/admin/accounts", s.rootOnly(s.listAccounts))
		r.Post("/admin/accounts/{id}/role", s.rootOnly(s.setRole))
		r.Post("/admin/accounts/{id}/disabled", s.rootOnly(s.setDisabled))
		r.Post("/admin/accounts/{id}/reset-password", s.rootOnly(s.resetPassword))
		r.Get("/admin/invites", s.rootOnly(s.listInvites))
		r.Post("/admin/invites", s.rootOnly(s.createInvite))
		r.Post("/admin/invites/{id}/revoke", s.rootOnly(s.revokeInvite))
	})
}

func cookieToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

func setSessionCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     basePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func startedSession(w http.ResponseWriter, token string) {
	setSessionCookie(w, token, int(auth.SessionTTL.Seconds()))
}

// requirePageLogin sends anyone not logged in to the login page.
func (s *Server) requirePageLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := cookieToken(r)
		account, err := s.auth.Authenticate(token)
		switch {
		case errors.Is(err, auth.ErrNoSession):
			target := basePath + "/login?next=" + url.QueryEscape(r.URL.RequestURI())
			http.Redirect(w, r, target, http.StatusFound)
		case err != nil:
			slog.Default().Error("check session", "error", err)
			http.Error(w, "accounts unavailable", http.StatusServiceUnavailable)
		case account.MustChange:
			http.Redirect(w, r, basePath+"/login?change=1", http.StatusFound)
		default:
			startedSession(w, token) // keeps the cookie as long as the session
			next.ServeHTTP(w, r)
		}
	})
}

// safeNext is where to go after logging in: next if it's a page of this site,
// otherwise the game, so a link can't send someone elsewhere.
func safeNext(next string) string {
	p, _, _ := strings.Cut(next, "?")
	switch {
	case p != basePath && !strings.HasPrefix(p, basePath+"/"),
		strings.ContainsAny(next, "\\\r\n\t"),
		strings.Contains(p, "//"),
		path.Clean(p) != strings.TrimSuffix(p, "/"),
		path.Clean(p) == basePath+"/login":
		return basePath
	}
	return next
}

// serveLogin is the page for logging in, registering and setting a first
// password. It holds none of the game.
func (s *Server) serveLogin(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	account, err := s.auth.Authenticate(cookieToken(r))
	mustChange := false
	if err == nil {
		if !account.MustChange {
			http.Redirect(w, r, next, http.StatusFound)
			return
		}
		mustChange = true
	}
	nextJSON, _ := json.Marshal(next) // escapes < and >, so it's safe in a script
	changeJSON, _ := json.Marshal(mustChange)
	page := strings.NewReplacer("__NEXT__", string(nextJSON), "__MUST_CHANGE__", string(changeJSON)).Replace(loginPage)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.Write([]byte(page))
}

// sameOriginPosts refuses changes asked for by another site.
func sameOriginPosts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && !checkOrigin(r) {
			writeAuthError(w, http.StatusForbidden, "请求来源无效")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type accountHandler func(w http.ResponseWriter, r *http.Request, account auth.Account)

// loggedIn is for APIs that need a login. allowMustChange lets through someone
// who hasn't yet set their new password.
func (s *Server) loggedIn(allowMustChange bool, h accountHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		account, err := s.auth.Authenticate(cookieToken(r))
		switch {
		case errors.Is(err, auth.ErrNoSession):
			writeAuthError(w, http.StatusUnauthorized, auth.ErrNoSession.Error())
		case err != nil:
			slog.Default().Error("check session", "error", err)
			writeAuthError(w, http.StatusServiceUnavailable, "账号服务暂时不可用")
		case account.MustChange && !allowMustChange:
			writeAuthError(w, http.StatusForbidden, "请先修改密码")
		default:
			h(w, r, account)
		}
	}
}

func (s *Server) rootOnly(h accountHandler) http.HandlerFunc {
	return s.loggedIn(false, func(w http.ResponseWriter, r *http.Request, account auth.Account) {
		if account.Role != auth.RoleRoot {
			writeAuthError(w, http.StatusForbidden, "没有权限")
			return
		}
		h(w, r, account)
	})
}

func writeAuthJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Default().Error("encode response", "error", err)
	}
}

func writeAuthError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// writeAccountError says what went wrong with an account change.
func writeAccountError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, auth.ErrBadUsername), errors.Is(err, auth.ErrBadPassword), errors.Is(err, auth.ErrBadRole):
		code = http.StatusBadRequest
	case errors.Is(err, auth.ErrUsernameTaken):
		code = http.StatusConflict
	case errors.Is(err, auth.ErrBadInvite), errors.Is(err, auth.ErrRootProtected):
		code = http.StatusForbidden
	case errors.Is(err, auth.ErrNoAccount), errors.Is(err, auth.ErrNoInvite):
		code = http.StatusNotFound
	default:
		slog.Default().Error("account change", "error", err)
		writeAuthError(w, code, "服务器错误")
		return
	}
	writeAuthError(w, code, err.Error())
}

func readAuthBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAuthBody)).Decode(v); err != nil {
		writeAuthError(w, http.StatusBadRequest, "请求格式错误")
		return false
	}
	return true
}

// limitKeys are what failures count against: the address, and the username
// whatever its case.
func limitKeys(kind string, r *http.Request, username string) []string {
	return []string{kind + "-ip:" + clientIP(r, trustProxy), kind + "-user:" + strings.ToLower(username)}
}

func (s *Server) limited(w http.ResponseWriter, keys []string) bool {
	for _, k := range keys {
		if !s.limiter.Allow(k) {
			writeAuthError(w, http.StatusTooManyRequests, "尝试次数过多，请 15 分钟后再试")
			return true
		}
	}
	return false
}

func (s *Server) failed(keys []string) {
	for _, k := range keys {
		s.limiter.Fail(k)
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Password string }
	if !readAuthBody(w, r, &in) {
		return
	}
	keys := limitKeys("login", r, in.Username)
	if s.limited(w, keys) {
		return
	}
	token, account, err := s.auth.Login(in.Username, in.Password)
	if errors.Is(err, auth.ErrBadLogin) {
		s.failed(keys)
		writeAuthError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if err != nil {
		writeAccountError(w, err)
		return
	}
	s.limiter.Reset(keys[1])
	startedSession(w, token)
	writeAuthJSON(w, map[string]any{"account": account})
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Password, Invite string }
	if !readAuthBody(w, r, &in) {
		return
	}
	keys := limitKeys("register", r, in.Username)
	if s.limited(w, keys) {
		return
	}
	account, err := s.auth.Register(in.Username, in.Password, in.Invite)
	if errors.Is(err, auth.ErrBadInvite) {
		s.failed(keys)
	}
	if err != nil {
		writeAccountError(w, err)
		return
	}
	token, err := s.auth.StartSession(account.ID)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	startedSession(w, token)
	writeAuthJSON(w, map[string]any{"account": account})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, account auth.Account) {
	if err := s.auth.Logout(cookieToken(r)); err != nil {
		writeAccountError(w, err)
		return
	}
	s.hub.kick(account.ID)
	setSessionCookie(w, "", -1)
	writeAuthJSON(w, struct{}{})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, account auth.Account) {
	writeAuthJSON(w, map[string]any{"account": account})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request, account auth.Account) {
	var in struct{ Current, Password string }
	if !readAuthBody(w, r, &in) {
		return
	}
	keys := limitKeys("password", r, account.ID)
	if s.limited(w, keys) {
		return
	}
	if !account.MustChange && !s.auth.CheckPassword(account.ID, in.Current) {
		s.failed(keys)
		writeAuthError(w, http.StatusForbidden, "当前密码错误")
		return
	}
	if err := s.auth.ChangePassword(account.ID, in.Password); err != nil {
		writeAccountError(w, err)
		return
	}
	s.hub.kick(account.ID)
	token, err := s.auth.StartSession(account.ID)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	account, err = s.auth.Get(account.ID)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	startedSession(w, token)
	writeAuthJSON(w, map[string]any{"account": account})
}

func (s *Server) changeUsername(w http.ResponseWriter, r *http.Request, account auth.Account) {
	var in struct{ Username string }
	if !readAuthBody(w, r, &in) {
		return
	}
	if s.hub.accountAtTable(account.ID) {
		writeAuthError(w, http.StatusConflict, "请先离开牌桌再改名")
		return
	}
	if err := s.auth.Rename(account.ID, in.Username); err != nil {
		writeAccountError(w, err)
		return
	}
	account, err := s.auth.Get(account.ID)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	writeAuthJSON(w, map[string]any{"account": account})
}

func (s *Server) listAccounts(w http.ResponseWriter, r *http.Request, _ auth.Account) {
	list, err := s.auth.ListAccounts()
	if err != nil {
		writeAccountError(w, err)
		return
	}
	writeAuthJSON(w, map[string]any{"accounts": list})
}

func (s *Server) setRole(w http.ResponseWriter, r *http.Request, _ auth.Account) {
	var in struct{ Role auth.Role }
	if !readAuthBody(w, r, &in) {
		return
	}
	if err := s.auth.SetRole(chi.URLParam(r, "id"), in.Role); err != nil {
		writeAccountError(w, err)
		return
	}
	writeAuthJSON(w, struct{}{})
}

func (s *Server) setDisabled(w http.ResponseWriter, r *http.Request, _ auth.Account) {
	var in struct{ Disabled bool }
	if !readAuthBody(w, r, &in) {
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.auth.SetDisabled(id, in.Disabled); err != nil {
		writeAccountError(w, err)
		return
	}
	if in.Disabled {
		s.hub.kick(id)
	}
	writeAuthJSON(w, struct{}{})
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request, _ auth.Account) {
	id := chi.URLParam(r, "id")
	password, err := s.auth.ResetPassword(id)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	s.hub.kick(id)
	writeAuthJSON(w, map[string]string{"password": password})
}

func (s *Server) listInvites(w http.ResponseWriter, r *http.Request, _ auth.Account) {
	list, err := s.auth.ListInvites()
	if err != nil {
		writeAccountError(w, err)
		return
	}
	writeAuthJSON(w, map[string]any{"invites": list})
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request, account auth.Account) {
	code, err := s.auth.CreateInvite(account.ID, auth.DefaultInviteTTL)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	writeAuthJSON(w, map[string]string{"code": code})
}

func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request, _ auth.Account) {
	if err := s.auth.RevokeInvite(chi.URLParam(r, "id")); err != nil {
		writeAccountError(w, err)
		return
	}
	writeAuthJSON(w, struct{}{})
}
