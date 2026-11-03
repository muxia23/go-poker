package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/evanofslack/go-poker/table"
	"github.com/gorilla/websocket"
)

// dialAs connects to the game as a logged in account.
func (e *authEnv) dialAs(token string) *client {
	e.t.Helper()
	url := "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/poker/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, http.Header{"Cookie": {sessionCookie + "=" + token}})
	if err != nil {
		e.t.Fatal(err)
	}
	return readClient(e.t, conn)
}

// admin registers name and makes it an admin.
func (e *authEnv) admin(name string) (token, id string) {
	e.t.Helper()
	token, id = e.user(name)
	if err := e.auth.SetRole(id, "admin"); err != nil {
		e.t.Fatal(err)
	}
	return token, id
}

func TestUsersCantCreateTables(t *testing.T) {
	e := newAuthEnv(t)
	token, _ := e.user("alice")
	c := e.dialAs(token)
	c.send(createMsg("t", "alice"))
	if got := c.wait(ActionError, nil); !strings.Contains(string(got.raw), "only admins can create tables") {
		t.Fatalf("got %s", got.raw)
	}
	if len(e.hub.allTables()) != 0 {
		t.Fatal("a table was created")
	}
}

func TestRoleTakesEffectOnOpenConnections(t *testing.T) {
	e := newAuthEnv(t)
	token, id := e.user("alice")
	c := e.dialAs(token)
	if err := e.auth.SetRole(id, "admin"); err != nil {
		t.Fatal(err)
	}
	c.send(createMsg("t", "alice"))
	c.wait(ActionJoined, nil)
}

func TestNameComesFromTheAccount(t *testing.T) {
	e := newAuthEnv(t)
	token, _ := e.admin("alice")
	c := e.dialAs(token)
	c.send(createMsg("t", "someone-else"))
	var joined struct {
		Username string `json:"username"`
	}
	c.wait(ActionJoined, nil).decode(t, &joined)
	if joined.Username != "alice" {
		t.Errorf("joined as %q", joined.Username)
	}
}

func TestTablesAreNeverListed(t *testing.T) {
	e := newAuthEnv(t)
	token, _ := e.admin("alice")
	c := e.dialAs(token)
	settings := table.DefaultSettings
	settings.Public = true
	b, _ := json.Marshal(settings)
	c.send(`{"action":"create-table","tablename":"t","settings":` + string(b) + `}`)
	c.wait(ActionJoined, nil)
	a := e.hub.table("t")
	var public bool
	a.inspect(func() { public = a.state.Settings.Public })
	if public {
		t.Error("the table is public")
	}
	c.send(`{"action":"list-tables"}`)
	var list struct {
		Tables []table.Summary `json:"tables"`
	}
	c.wait(ActionTables, nil).decode(t, &list)
	if len(list.Tables) != 0 {
		t.Errorf("listed %+v", list.Tables)
	}
}

func TestBotsCantUseAccountNames(t *testing.T) {
	e := newAuthEnv(t)
	token, _ := e.admin("alice")
	e.user("bob")
	c := e.dialAs(token)
	c.send(createMsg("t", ""))
	c.wait(ActionJoined, nil)
	c.send(`{"action":"add-bot","tablename":"t","seat":1,"amount":1000,"name":"BOB","level":"easy"}`)
	if got := c.wait(ActionError, nil); !strings.Contains(string(got.raw), "account") {
		t.Errorf("got %s", got.raw)
	}
}

func TestKickClosesEveryConnection(t *testing.T) {
	e := newAuthEnv(t)
	token, id := e.user("alice")
	other, _ := e.user("bob")
	a, b := e.dialAs(token), e.dialAs(token)
	keep := e.dialAs(other)
	eventually(t, func() bool { return e.hub.clientCount() == 3 }, "clients to register")
	e.hub.kick(id)
	a.waitClosed()
	b.waitClosed()
	keep.send(`{"action":"list-tables"}`)
	keep.wait(ActionTables, nil)
}

func TestDisablingKicks(t *testing.T) {
	e := newAuthEnv(t)
	root := e.rootToken()
	token, id := e.user("alice")
	c := e.dialAs(token)
	eventually(t, func() bool { return e.hub.clientCount() == 1 }, "client to register")
	if res, _ := e.post("/poker/api/admin/accounts/"+id+"/disabled", root, map[string]bool{"disabled": true}); res.StatusCode != 200 {
		t.Fatalf("disable: %d", res.StatusCode)
	}
	c.waitClosed()
	url := "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/poker/ws"
	if _, res, err := websocket.DefaultDialer.Dial(url, http.Header{"Cookie": {sessionCookie + "=" + token}}); err == nil || res.StatusCode != 401 {
		t.Errorf("redial: %v %v", res, err)
	}
	if res, _ := e.post("/poker/api/auth/login", "", map[string]string{"username": "alice", "password": "password1"}); res.StatusCode != 401 {
		t.Errorf("login: %d", res.StatusCode)
	}
}

func TestRenameWaitsUntilYouLeaveTheTable(t *testing.T) {
	e := newAuthEnv(t)
	token, _ := e.admin("alice")
	otherToken, _ := e.user("bob")
	c := e.dialAs(token)
	c.send(createMsg("t", ""))
	c.wait(ActionJoined, nil)
	other := e.dialAs(otherToken)
	other.send(joinMsg("t", ""))
	other.wait(ActionJoined, nil)

	if res, out := e.post("/poker/api/auth/username", token, map[string]string{"username": "carol"}); res.StatusCode != 409 {
		t.Fatalf("at the table: %d %v", res.StatusCode, out)
	}
	c.send(`{"action":"leave-table","tablename":"t"}`)
	eventually(t, func() bool {
		return !e.hub.accountAtTable(accountID(e, "alice"))
	}, "to leave the table")
	res, out := e.post("/poker/api/auth/username", token, map[string]string{"username": "carol"})
	if res.StatusCode != 200 || out["account"].(map[string]any)["username"] != "carol" {
		t.Fatalf("after leaving: %d %v", res.StatusCode, out)
	}

	// back at the old table they're who its ledger knows
	back := e.dialAs(token)
	back.send(joinMsg("t", ""))
	var joined struct {
		Username string `json:"username"`
	}
	back.wait(ActionJoined, nil).decode(t, &joined)
	if joined.Username != "alice" {
		t.Errorf("old table: %q", joined.Username)
	}
	// and a new table has the new name
	back.send(`{"action":"leave-table","tablename":"t"}`)
	time.Sleep(50 * time.Millisecond)
	back.send(createMsg("t2", ""))
	var newer struct {
		Username string `json:"username"`
	}
	back.wait(ActionJoined, nil).decode(t, &newer)
	if newer.Username != "carol" {
		t.Errorf("new table: %q", newer.Username)
	}
}

// waitClosed waits for the server to close the connection.
func (c *client) waitClosed() {
	c.t.Helper()
	timeout := time.After(c.timeout)
	for {
		select {
		case _, ok := <-c.msgs:
			if !ok {
				return
			}
		case <-timeout:
			c.t.Fatal("still connected")
		}
	}
}

func accountID(e *authEnv, name string) string {
	var id string
	e.hub.store.(*sqlStore).DB().QueryRow(`select id from accounts where username = $1`, name).Scan(&id)
	return id
}

func TestAnotherDeviceTakesTheSeat(t *testing.T) {
	e := newAuthEnv(t)
	token, _ := e.admin("alice")
	first := e.dialAs(token)
	first.send(createMsg("t", ""))
	first.wait(ActionJoined, nil)
	second := e.dialAs(token)
	second.send(joinMsg("t", ""))
	second.wait(ActionJoined, nil)
	first.waitClosed()
	a := e.hub.table("t")
	var people int
	a.inspect(func() { people = len(a.state.People) })
	if people != 1 {
		t.Errorf("%d people", people)
	}
}
