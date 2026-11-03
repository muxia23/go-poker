package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/evanofslack/go-poker/auth"
	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

// authEnv is the site's routes on a real database, with root set up.
type authEnv struct {
	t    *testing.T
	srv  *httptest.Server
	hub  *Hub
	auth *auth.Service
}

const rootPassword = "rootpassword"

func newAuthEnv(t *testing.T) *authEnv {
	t.Helper()
	dir := t.TempDir()
	store, err := openStore(t.Context(), "sqlite:"+filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.(*sqlStore).Close() })
	static := filepath.Join(dir, "static")
	os.MkdirAll(filepath.Join(static, "_next"), 0o755)
	os.WriteFile(filepath.Join(static, "index.html"), []byte("the game"), 0o644)
	os.WriteFile(filepath.Join(static, "_next", "x.js"), []byte("game code"), 0o644)

	svc := auth.New(store.(*sqlStore).DB())
	if err := svc.EnsureRoot("root", rootPassword); err != nil {
		t.Fatal(err)
	}
	hub := newHub(store)
	hub.auth = svc
	go hub.run()
	s := &Server{router: chi.NewRouter(), hub: hub, staticDir: static, auth: svc, limiter: auth.NewLimiter(loginAttempts, loginWindow)}
	s.mountSocket()
	s.mountAuth()
	s.mountStatic()
	srv := httptest.NewServer(s.router)
	t.Cleanup(srv.Close)
	return &authEnv{t, srv, hub, svc}
}

// noFollow doesn't follow redirects, so tests can see them.
var noFollow = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func (e *authEnv) get(path, cookie string) *http.Response {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+path, nil)
	if cookie != "" {
		req.Header.Set("Cookie", sessionCookie+"="+cookie)
	}
	res, err := noFollow.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	res.Body.Close()
	return res
}

// post returns the response and its decoded JSON.
func (e *authEnv) post(path, cookie string, body any) (*http.Response, map[string]any) {
	e.t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", e.srv.URL+path, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", e.srv.URL)
	if cookie != "" {
		req.Header.Set("Cookie", sessionCookie+"="+cookie)
	}
	res, err := noFollow.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res, out
}

func tokenFrom(t *testing.T, res *http.Response) string {
	t.Helper()
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			return c.Value
		}
	}
	t.Fatalf("no session cookie: %v", res.Header)
	return ""
}

// rootToken logs root in and sets its first password.
func (e *authEnv) rootToken() string {
	e.t.Helper()
	res, _ := e.post("/poker/api/auth/login", "", map[string]string{"username": "root", "password": rootPassword})
	if res.StatusCode != 200 {
		e.t.Fatalf("root login: %d", res.StatusCode)
	}
	res, _ = e.post("/poker/api/auth/password", tokenFrom(e.t, res), map[string]string{"password": rootPassword + "2"})
	if res.StatusCode != 200 {
		e.t.Fatalf("root password: %d", res.StatusCode)
	}
	return tokenFrom(e.t, res)
}

// user registers name through the API and returns its session and account id.
func (e *authEnv) user(name string) (token, id string) {
	e.t.Helper()
	var root string
	row := e.hub.store.(*sqlStore).DB().QueryRow(`select id from accounts where role = 'root'`)
	if err := row.Scan(&root); err != nil {
		e.t.Fatal(err)
	}
	code, err := e.auth.CreateInvite(root, time.Hour)
	if err != nil {
		e.t.Fatal(err)
	}
	res, out := e.post("/poker/api/auth/register", "", map[string]string{"username": name, "password": "password1", "invite": code})
	if res.StatusCode != 200 {
		e.t.Fatalf("register %s: %d %v", name, res.StatusCode, out)
	}
	return tokenFrom(e.t, res), out["account"].(map[string]any)["id"].(string)
}

func wantRedirect(t *testing.T, res *http.Response, prefix string) {
	t.Helper()
	if res.StatusCode != http.StatusFound || !strings.HasPrefix(res.Header.Get("Location"), prefix) {
		t.Errorf("got %d to %q, want a redirect to %q", res.StatusCode, res.Header.Get("Location"), prefix)
	}
}

func TestNothingWithoutLogin(t *testing.T) {
	e := newAuthEnv(t)
	if res := e.get("/", ""); res.StatusCode != 404 {
		t.Errorf("/: %d", res.StatusCode)
	}
	if res := e.get("/ws", ""); res.StatusCode != 404 {
		t.Errorf("/ws: %d", res.StatusCode)
	}
	wantRedirect(t, e.get("/poker", ""), "/poker/login?next=%2Fpoker")
	wantRedirect(t, e.get("/poker?table=a", ""), "/poker/login?next=%2Fpoker%3Ftable%3Da")
	wantRedirect(t, e.get("/poker/_next/x.js", ""), "/poker/login")
	wantRedirect(t, e.get("/poker/accounts", ""), "/poker/login")
	if res := e.get("/poker/login", ""); res.StatusCode != 200 {
		t.Errorf("login page: %d", res.StatusCode)
	}
	for _, p := range []string{"/poker/api/auth/me", "/poker/api/admin/accounts", "/poker/api/admin/invites"} {
		if res := e.get(p, ""); res.StatusCode != 401 {
			t.Errorf("%s: %d", p, res.StatusCode)
		}
	}
	url := "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/poker/ws"
	_, res, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil || res.StatusCode != 401 {
		t.Errorf("websocket: %v %v", res, err)
	}
	// a made up session is nobody
	if res := e.get("/poker/_next/x.js", "made-up"); res.StatusCode != http.StatusFound {
		t.Errorf("made up session: %d", res.StatusCode)
	}
}

func TestLoggedInGetsTheGame(t *testing.T) {
	e := newAuthEnv(t)
	token, _ := e.user("alice")
	for _, p := range []string{"/poker", "/poker/", "/poker/_next/x.js", "/poker/accounts"} {
		if res := e.get(p, token); res.StatusCode != 200 {
			t.Errorf("%s: %d", p, res.StatusCode)
		}
	}
	wantRedirect(t, e.get("/poker/login", token), "/poker")
	url := "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/poker/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, http.Header{"Cookie": {sessionCookie + "=" + token}})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}

func TestSessionCookieAttributes(t *testing.T) {
	e := newAuthEnv(t)
	res, _ := e.post("/poker/api/auth/login", "", map[string]string{"username": "root", "password": rootPassword})
	c := res.Cookies()[0]
	if c.Name != sessionCookie || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/poker" {
		t.Errorf("got %+v", c)
	}
}

func TestLoginNextStaysOnTheSite(t *testing.T) {
	for in, want := range map[string]string{
		"/poker?table=a":       "/poker?table=a",
		"/poker/accounts":      "/poker/accounts",
		"":                     "/poker",
		"https://evil.example": "/poker",
		"//evil.example":       "/poker",
		"/poker//evil.example": "/poker",
		"/pokerevil":           "/poker",
		"/poker/../admin":      "/poker",
		`/poker\evil`:          "/poker",
		"/poker/login":         "/poker",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("%q: got %q", in, got)
		}
	}
}

func TestCrossSitePostsAreRefused(t *testing.T) {
	e := newAuthEnv(t)
	req, _ := http.NewRequest("POST", e.srv.URL+"/poker/api/auth/login", strings.NewReader(`{"username":"root","password":"`+rootPassword+`"}`))
	req.Header.Set("Origin", "https://evil.example")
	res, err := noFollow.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Errorf("got %d", res.StatusCode)
	}
}

func TestOnlyRootManages(t *testing.T) {
	e := newAuthEnv(t)
	user, userID := e.user("alice")
	admin, adminID := e.user("bob")
	root := e.rootToken()

	if res, _ := e.post("/poker/api/admin/accounts/"+adminID+"/role", root, map[string]string{"role": "admin"}); res.StatusCode != 200 {
		t.Fatalf("make admin: %d", res.StatusCode)
	}
	for _, token := range []string{user, admin} {
		if res := e.get("/poker/api/admin/accounts", token); res.StatusCode != 403 {
			t.Errorf("list: %d", res.StatusCode)
		}
		if res := e.get("/poker/api/admin/invites", token); res.StatusCode != 403 {
			t.Errorf("invites: %d", res.StatusCode)
		}
		if res, _ := e.post("/poker/api/admin/invites", token, nil); res.StatusCode != 403 {
			t.Errorf("new invite: %d", res.StatusCode)
		}
		if res, _ := e.post("/poker/api/admin/accounts/"+userID+"/disabled", token, map[string]bool{"disabled": true}); res.StatusCode != 403 {
			t.Errorf("disable: %d", res.StatusCode)
		}
	}
	if res := e.get("/poker/api/admin/accounts", root); res.StatusCode != 200 {
		t.Errorf("root list: %d", res.StatusCode)
	}
	res, out := e.post("/poker/api/admin/invites", root, nil)
	code, _ := out["code"].(string)
	if res.StatusCode != 200 || code == "" {
		t.Fatalf("invite: %d %v", res.StatusCode, out)
	}
	if res, _ := e.post("/poker/api/auth/register", "", map[string]string{"username": "carol", "password": "password1", "invite": code}); res.StatusCode != 200 {
		t.Errorf("register: %d", res.StatusCode)
	}
	var rootID string
	e.hub.store.(*sqlStore).DB().QueryRow(`select id from accounts where role = 'root'`).Scan(&rootID)
	if res, _ := e.post("/poker/api/admin/accounts/"+rootID+"/disabled", root, map[string]bool{"disabled": true}); res.StatusCode != 403 {
		t.Errorf("disable root: %d", res.StatusCode)
	}
	if res, _ := e.post("/poker/api/admin/accounts/"+userID+"/role", root, map[string]string{"role": "root"}); res.StatusCode != 403 {
		t.Errorf("second root: %d", res.StatusCode)
	}
}

func TestRegisterErrors(t *testing.T) {
	e := newAuthEnv(t)
	e.user("alice")
	if res, _ := e.post("/poker/api/auth/register", "", map[string]string{"username": "bob", "password": "password1", "invite": "wrong"}); res.StatusCode != 403 {
		t.Errorf("bad invite: %d", res.StatusCode)
	}
	if res, _ := e.post("/poker/api/auth/register", "", map[string]string{"username": "b", "password": "password1", "invite": "wrong"}); res.StatusCode != 400 {
		t.Errorf("bad name: %d", res.StatusCode)
	}
}

func TestMustChangeFirst(t *testing.T) {
	e := newAuthEnv(t)
	res, _ := e.post("/poker/api/auth/login", "", map[string]string{"username": "root", "password": rootPassword})
	token := tokenFrom(t, res)
	wantRedirect(t, e.get("/poker", token), "/poker/login?change=1")
	if res := e.get("/poker/login", token); res.StatusCode != 200 {
		t.Errorf("login page: %d", res.StatusCode)
	}
	if res := e.get("/poker/api/auth/me", token); res.StatusCode != 200 {
		t.Errorf("me: %d", res.StatusCode)
	}
	if res := e.get("/poker/api/admin/accounts", token); res.StatusCode != 403 {
		t.Errorf("accounts: %d", res.StatusCode)
	}
	url := "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/poker/ws"
	_, wres, err := websocket.DefaultDialer.Dial(url, http.Header{"Cookie": {sessionCookie + "=" + token}})
	if err == nil || wres.StatusCode != 401 {
		t.Errorf("websocket: %v %v", wres, err)
	}
	// no current password needed this once
	res, _ = e.post("/poker/api/auth/password", token, map[string]string{"password": "newpassword"})
	if res.StatusCode != 200 {
		t.Fatalf("change: %d", res.StatusCode)
	}
	if res := e.get("/poker", tokenFrom(t, res)); res.StatusCode != 200 {
		t.Errorf("after change: %d", res.StatusCode)
	}
}

func TestChangePassword(t *testing.T) {
	e := newAuthEnv(t)
	token, _ := e.user("alice")
	if res, _ := e.post("/poker/api/auth/password", token, map[string]string{"current": "wrongpassword", "password": "password2"}); res.StatusCode != 403 {
		t.Errorf("wrong current: %d", res.StatusCode)
	}
	res, _ := e.post("/poker/api/auth/password", token, map[string]string{"current": "password1", "password": "password2"})
	if res.StatusCode != 200 {
		t.Fatalf("change: %d", res.StatusCode)
	}
	if got := e.get("/poker", token); got.StatusCode != http.StatusFound {
		t.Errorf("old session: %d", got.StatusCode)
	}
	if got := e.get("/poker", tokenFrom(t, res)); got.StatusCode != 200 {
		t.Errorf("new session: %d", got.StatusCode)
	}
}

func TestLogout(t *testing.T) {
	e := newAuthEnv(t)
	token, _ := e.user("alice")
	if res, _ := e.post("/poker/api/auth/logout", token, nil); res.StatusCode != 200 {
		t.Fatalf("logout: %d", res.StatusCode)
	}
	if res := e.get("/poker/api/auth/me", token); res.StatusCode != 401 {
		t.Errorf("after logout: %d", res.StatusCode)
	}
}

func TestLoginIsLimited(t *testing.T) {
	e := newAuthEnv(t)
	for range loginAttempts {
		if res, _ := e.post("/poker/api/auth/login", "", map[string]string{"username": "root", "password": "wrongpassword"}); res.StatusCode != 401 {
			t.Fatalf("got %d", res.StatusCode)
		}
	}
	if res, _ := e.post("/poker/api/auth/login", "", map[string]string{"username": "root", "password": rootPassword}); res.StatusCode != 429 {
		t.Errorf("got %d", res.StatusCode)
	}
}

func TestNoDatabaseNoServer(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := New(); err == nil {
		t.Fatal("started with nowhere to keep accounts")
	}
}

func TestResetRootFromTheCommandLine(t *testing.T) {
	url := "sqlite:" + filepath.Join(t.TempDir(), "t.db")
	t.Setenv("DATABASE_URL", url)
	store, err := openStore(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	svc := auth.New(store.(*sqlStore).DB())
	if err := svc.EnsureRoot("root", rootPassword); err != nil {
		t.Fatal(err)
	}
	store.(*sqlStore).Close()

	name, password, err := ResetRoot(t.Context())
	if err != nil || name != "root" {
		t.Fatal(name, err)
	}
	store, err = openStore(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer store.(*sqlStore).Close()
	if _, _, err := auth.New(store.(*sqlStore).DB()).Login("root", password); err != nil {
		t.Error(err)
	}
}
