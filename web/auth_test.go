package web

import (
	"github.com/DavidMasin/galaxia-fms/model"
	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The real work factor makes every login in these tests take a noticeable part of a second.
func init() {
	model.PasswordIterations = 1000
}

// request sends a request with a form or JSON body and the given headers.
func (web *Web) request(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	req, _ := http.NewRequest(method, path, strings.NewReader(body))
	if method == "POST" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	web.newHandler().ServeHTTP(recorder, req)
	return recorder
}

// loginAs logs in and returns the headers that carry the session.
func loginAs(t *testing.T, web *Web, username, password string) map[string]string {
	recorder := web.postHttpResponse("/login", "username="+username+"&password="+url.QueryEscape(password))
	require.Equal(t, 303, recorder.Code, recorder.Body.String())
	cookie := recorder.Header().Get("Set-Cookie")
	require.Contains(t, cookie, "session_token=")
	return map[string]string{"Cookie": strings.SplitN(cookie, ";", 2)[0]}
}

func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// The pages and API routes that show what each kind of access is for. The scoring panel is one of the panels of
// the game the test arena starts with.
const (
	adminPage    = "/setup/users"
	scorerPage   = "/panels/scoring/red_near"
	refereePage  = "/panels/referee"
	openPage     = "/match_review"
	readRoute    = "/api/v1/rankings"
	publicRoute  = "/api/v1/time"
	whoAmIRoute  = "/api/v1/auth"
	forbiddenTag = "Not allowed"
)

func TestLoginsOffEverythingIsOpen(t *testing.T) {
	web := setupTestWeb(t)
	for _, path := range []string{adminPage, scorerPage, refereePage, openPage, readRoute, publicRoute} {
		assert.Equal(t, 200, web.getHttpResponse(path).Code, path)
	}
	response := decodeJson(t, web.getHttpResponse(whoAmIRoute))
	assert.Equal(t, false, response["required"])
	assert.Nil(t, response["identity"])

	// A token changes nothing while logins are off, valid or not.
	assert.Equal(t, 200, web.request("GET", readRoute, "", bearer("gfms_nonsense")).Code)
}

func TestRolesDecideWhatOpens(t *testing.T) {
	web := setupTestWeb(t)
	web.arena.EventSettings.AdminPassword = "the admin password"
	for username, role := range map[string]model.Role{
		"anna": model.RoleAdmin, "sam": model.RoleScorer, "rex": model.RoleReferee, "rita": model.RoleReadOnly,
	} {
		_, err := web.arena.Database.CreateUser(username, "password-of-"+username, role)
		require.Nil(t, err)
	}

	// Nobody in particular: pages go to the login page, the API says 401, and what was always open stays open.
	for _, path := range []string{adminPage, scorerPage, refereePage, "/match_review/1/edit", "/match_play"} {
		recorder := web.getHttpResponse(path)
		assert.Equal(t, 307, recorder.Code, path)
		assert.Equal(t, "/login?redirect="+url.QueryEscape(path), recorder.Header().Get("Location"))
	}
	recorder := web.getHttpResponse(readRoute)
	assert.Equal(t, 401, recorder.Code)
	assert.Equal(t, `Bearer realm="galaxia-fms"`, recorder.Header().Get("WWW-Authenticate"))
	assert.Contains(t, recorder.Body.String(), `"error"`)
	for _, path := range []string{
		openPage, publicRoute, "/api/v1/health", "/api/v1/openapi.yaml", "/api/v1/openapi.json", whoAmIRoute, "/",
		"/match_logs", "/reports/csv/rankings",
	} {
		assert.Equal(t, 200, web.getHttpResponse(path).Code, path)
	}

	// 200 is "may open it", 403 is the page that says the role may not.
	for _, test := range []struct {
		username                         string
		admin, scorer, referee, readCode int
	}{
		{"admin", 200, 200, 200, 200},
		{"anna", 200, 200, 200, 200},
		{"sam", 403, 200, 403, 200},
		{"rex", 403, 403, 200, 200},
		{"rita", 403, 403, 403, 200},
	} {
		password := "password-of-" + test.username
		if test.username == "admin" {
			password = "the admin password"
		}
		session := loginAs(t, web, test.username, password)
		for path, code := range map[string]int{
			adminPage: test.admin, scorerPage: test.scorer, refereePage: test.referee, readRoute: test.readCode,
			"/match_play": test.admin, "/setup/settings": test.admin,
		} {
			recorder = web.request("GET", path, "", session)
			assert.Equal(t, code, recorder.Code, test.username+" "+path)
			if code == 403 {
				assert.Contains(t, recorder.Body.String(), forbiddenTag)
				assert.Contains(t, recorder.Body.String(), "logged in as <strong>"+test.username+"</strong>")
				// The link back to the login page carries the page asked for, escaped (in either case of hex).
				assert.Contains(
					t, strings.ToLower(recorder.Body.String()), strings.ToLower("/login?redirect="+url.QueryEscape(path)),
				)
			}
		}
		// Changing a game through the API needs an admin, and the API answers in JSON.
		recorder = web.request("PUT", "/api/v1/games/some-game", "id: some-game", session)
		if test.admin == 200 {
			assert.NotContains(t, []int{401, 403}, recorder.Code, test.username)
		} else {
			assert.Equal(t, 403, recorder.Code, test.username)
			assert.Contains(t, recorder.Body.String(), "it needs admin")
		}
		response := decodeJson(t, web.request("GET", whoAmIRoute, "", session))
		assert.Equal(t, true, response["required"])
		assert.Equal(t, test.username, response["identity"].(map[string]any)["name"])
	}

	// Match review edits are the scorer's; the referee cannot post them.
	scorer, referee := loginAs(t, web, "sam", "password-of-sam"), loginAs(t, web, "rex", "password-of-rex")
	assert.Equal(t, 403, web.request("POST", "/match_review/1/edit", "", referee).Code)
	assert.NotContains(t, []int{307, 401, 403}, web.request("POST", "/match_review/1/edit", "", scorer).Code)
}

func TestPanelWebsocketsNeedTheRole(t *testing.T) {
	web := setupTestWeb(t)
	web.arena.EventSettings.AdminPassword = "the admin password"
	_, err := web.arena.Database.CreateUser("sam", "password-of-sam", model.RoleScorer)
	require.Nil(t, err)
	scorer := loginAs(t, web, "sam", "password-of-sam")

	server, wsUrl := web.startTestServer()
	defer server.Close()
	dial := func(path string, headers map[string]string) (*gorillawebsocket.Conn, *http.Response, error) {
		header := http.Header{}
		for key, value := range headers {
			header.Set(key, value)
		}
		return gorillawebsocket.DefaultDialer.Dial(wsUrl+path, header)
	}

	_, response, err := dial(scorerPage+"/websocket", nil)
	assert.NotNil(t, err)
	assert.Equal(t, 307, response.StatusCode)
	_, response, err = dial("/panels/referee/websocket", scorer)
	assert.NotNil(t, err)
	assert.Equal(t, 403, response.StatusCode)
	conn, _, err := dial(scorerPage+"/websocket", scorer)
	if assert.Nil(t, err) {
		conn.Close()
	}
}

func TestApiTokens(t *testing.T) {
	web := setupTestWeb(t)
	web.arena.EventSettings.AdminPassword = "the admin password"
	reader, readToken, err := web.arena.Database.CreateApiToken("Stream overlay", model.RoleReadOnly)
	require.Nil(t, err)
	_, adminToken, err := web.arena.Database.CreateApiToken("Field script", model.RoleAdmin)
	require.Nil(t, err)

	// Reading works with any token; changing a game needs the admin one.
	for _, path := range []string{
		readRoute, "/api/v1/game", "/api/v1/match", "/api/v1/matches", "/api/v1/teams", "/api/v1/alliances",
		"/api/v1/events", "/api/v1/games", "/api/v1/exports/matches.csv",
	} {
		assert.Equal(t, 401, web.getHttpResponse(path).Code, path)
		assert.Equal(t, 200, web.request("GET", path, "", bearer(readToken)).Code, path)
	}
	recorder := web.request("PUT", "/api/v1/games/some-game", "id: some-game", bearer(readToken))
	assert.Equal(t, 403, recorder.Code)
	assert.JSONEq(t, `{"error":"the read-only role cannot do this; it needs admin"}`, recorder.Body.String())
	recorder = web.request("PUT", "/api/v1/games/some-game", "id: some-game", bearer(adminToken))
	assert.NotContains(t, []int{401, 403}, recorder.Code)

	response := decodeJson(t, web.request("GET", whoAmIRoute, "", bearer(readToken)))
	assert.Equal(
		t, map[string]any{"name": "Stream overlay", "role": "read-only", "token": true}, response["identity"],
	)

	// A token that is wrong, cut short or revoked is nobody. A session cookie beside it does not rescue it.
	for _, wrong := range []string{"", "gfms_", readToken[:len(readToken)-2], readToken + "A", "not-a-token"} {
		assert.Equal(t, 401, web.request("GET", readRoute, "", bearer(wrong)).Code, wrong)
	}
	admin := loginAs(t, web, "admin", "the admin password")
	admin["Authorization"] = "Bearer gfms_wrong"
	assert.Equal(t, 401, web.request("GET", readRoute, "", admin).Code)

	// When a token was last used is recorded, at most once a minute.
	stored, _ := web.arena.Database.GetApiTokenByToken(readToken)
	firstUse := stored.LastUsedAt
	assert.WithinDuration(t, time.Now(), firstUse, 5*time.Second)
	web.request("GET", readRoute, "", bearer(readToken))
	stored, _ = web.arena.Database.GetApiTokenByToken(readToken)
	assert.Equal(t, firstUse, stored.LastUsedAt)

	assert.Nil(t, web.arena.Database.DeleteApiToken(reader.Id))
	assert.Equal(t, 401, web.request("GET", readRoute, "", bearer(readToken)).Code)

	// The devices' own routes use device tokens, not these: an API token is not a device.
	recorder = web.request("GET", "/api/v1/devices/some-device/out", "", bearer(adminToken))
	assert.NotEqual(t, 200, recorder.Code)
}

func TestLoginAndLogout(t *testing.T) {
	web := setupTestWeb(t)
	_, err := web.arena.Database.CreateUser("Anna", "password-of-anna", model.RoleAdmin)
	require.Nil(t, err)

	// A user alone turns logins on, and "admin" does not log in while the settings have no admin password.
	assert.Equal(t, 307, web.getHttpResponse(adminPage).Code)
	for _, body := range []string{
		"username=admin&password=", "username=admin&password=anything", "username=Anna&password=wrong-password",
		"username=nobody&password=password-of-anna", "username=&password=",
	} {
		recorder := web.postHttpResponse("/login", body)
		assert.Equal(t, 200, recorder.Code, body)
		assert.Contains(t, recorder.Body.String(), "Invalid login credentials.", body)
	}

	// Usernames ignore case; the session cookie is kept from scripts and from other sites.
	recorder := web.postHttpResponse("/login?redirect=%2Fsetup%2Fusers", "username=anna&password=password-of-anna")
	assert.Equal(t, 303, recorder.Code)
	assert.Equal(t, "/setup/users", recorder.Header().Get("Location"))
	cookie := recorder.Header().Get("Set-Cookie")
	assert.Contains(t, cookie, "HttpOnly")
	assert.Contains(t, cookie, "SameSite=Lax")
	assert.Contains(t, cookie, "Path=/")
	session := map[string]string{"Cookie": strings.SplitN(cookie, ";", 2)[0]}
	assert.Equal(t, 200, web.request("GET", adminPage, "", session).Code)
	response := decodeJson(t, web.request("GET", whoAmIRoute, "", session))
	assert.Equal(t, map[string]any{"name": "Anna", "role": "admin", "token": false}, response["identity"])

	// The login page only sends people on to this server.
	for _, redirect := range []string{"https://example.com/", "//example.com/", "example.com", "javascript:alert(1)"} {
		recorder = web.postHttpResponse(
			"/login?redirect="+url.QueryEscape(redirect), "username=anna&password=password-of-anna",
		)
		assert.Equal(t, 303, recorder.Code)
		assert.Equal(t, "/", recorder.Header().Get("Location"), redirect)
	}

	// Logging out ends the session on the server, not only in the browser.
	recorder = web.request("GET", "/logout", "", session)
	assert.Equal(t, 303, recorder.Code)
	assert.Contains(t, recorder.Header().Get("Set-Cookie"), "Max-Age=0")
	assert.Equal(t, 307, web.request("GET", adminPage, "", session).Code)
	assert.Equal(t, 303, web.getHttpResponse("/logout").Code)
}

func TestAdminPasswordLoginEndsWithThePassword(t *testing.T) {
	web := setupTestWeb(t)
	web.arena.EventSettings.AdminPassword = "the admin password"
	_, err := web.arena.Database.CreateUser("anna", "password-of-anna", model.RoleAdmin)
	require.Nil(t, err)
	admin := loginAs(t, web, "admin", "the admin password")
	assert.Equal(t, 200, web.request("GET", adminPage, "", admin).Code)

	// Logins stay on because of the user, but the old "admin" session is worth nothing.
	web.arena.EventSettings.AdminPassword = ""
	assert.Equal(t, 307, web.request("GET", adminPage, "", admin).Code)

	// A deleted user's session is worth nothing either.
	web.arena.EventSettings.AdminPassword = "the admin password"
	anna := loginAs(t, web, "anna", "password-of-anna")
	assert.Equal(t, 200, web.request("GET", adminPage, "", anna).Code)
	user, _ := web.arena.Database.GetUserByUsername("anna")
	assert.Nil(t, web.arena.Database.DeleteUser(user.Id))
	assert.Equal(t, 307, web.request("GET", adminPage, "", anna).Code)
}
