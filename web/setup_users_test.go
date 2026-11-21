package web

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"regexp"
	"strings"
	"testing"
)

func TestUsersPageTurnsLoginsOn(t *testing.T) {
	web := setupTestWeb(t)

	recorder := web.getHttpResponse("/setup/users")
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Logins are <strong>off</strong>")
	assert.Contains(t, recorder.Body.String(), "No users yet.")

	// Without an admin password, the first user must be an admin, or nobody could change anything afterwards.
	recorder = web.postHttpResponse("/setup/users", "username=sam&password=password-of-sam&role=scorer")
	assert.Equal(t, 400, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "nobody could log in as an admin afterwards")
	users, _ := web.arena.Database.GetAllUsers()
	assert.Empty(t, users)

	// The first admin turns logins on, and whoever made it is logged in as it.
	recorder = web.postHttpResponse("/setup/users", "username=anna&password=password-of-anna&role=admin")
	assert.Equal(t, 303, recorder.Code)
	assert.Contains(t, recorder.Header().Get("Location"), "you+are+logged+in+as+anna")
	cookie := recorder.Header().Get("Set-Cookie")
	require.Contains(t, cookie, "session_token=")
	anna := map[string]string{"Cookie": strings.SplitN(cookie, ";", 2)[0]}
	assert.Equal(t, 307, web.getHttpResponse("/setup/users").Code)
	recorder = web.request("GET", recorder.Header().Get("Location"), "", anna)
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "Logins are <strong>on</strong>")
	assert.Contains(t, recorder.Body.String(), "You are logged in as <strong>anna</strong> (admin)")
	assert.Contains(t, recorder.Body.String(), "Logins are now on, and you are logged in as anna.")

	// Later users need the admin, and get no session of their own from being made.
	assert.Equal(t, 307, web.postHttpResponse("/setup/users", "username=x&password=password-x&role=admin").Code)
	recorder = web.request("POST", "/setup/users", "username=sam&password=password-of-sam&role=scorer", anna)
	assert.Equal(t, 303, recorder.Code)
	assert.Empty(t, recorder.Header().Get("Set-Cookie"))
	loginAs(t, web, "sam", "password-of-sam")

	for body, message := range map[string]string{
		"username=sam&password=password-of-sam&role=scorer": "there is already a user named",
		"username=admin&password=password-admin&role=admin": "is the login of the admin password in the settings",
		"username=rex&password=short&role=referee":          "a password must be at least 8 characters long",
		"username=rex&password=password-of-rex&role=owner":  "is not a role",
		"username=two+words&password=password-x&role=admin": "a username is 1 to 40 letters",
	} {
		recorder = web.request("POST", "/setup/users", body, anna)
		assert.Equal(t, 400, recorder.Code, body)
		assert.Contains(t, recorder.Body.String(), message, body)
	}
}

func TestUsersPageKeepsAnAdmin(t *testing.T) {
	web := setupTestWeb(t)
	anna, err := web.arena.Database.CreateUser("anna", "password-of-anna", model.RoleAdmin)
	require.Nil(t, err)
	sam, err := web.arena.Database.CreateUser("sam", "password-of-sam", model.RoleScorer)
	require.Nil(t, err)
	session := loginAs(t, web, "anna", "password-of-anna")
	path := func(user *model.User, action string) string {
		return fmt.Sprintf("/setup/users/%d/%s", user.Id, action)
	}

	// The last admin can be neither demoted nor deleted while there is no admin password in the settings.
	recorder := web.request("POST", path(anna, "role"), "role=scorer", session)
	assert.Equal(t, 400, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "nobody could log in as an admin afterwards")
	recorder = web.request("POST", path(anna, "delete"), "", session)
	assert.Equal(t, 400, recorder.Code)
	users, _ := web.arena.Database.GetAllUsers()
	assert.Len(t, users, 2)

	// With a second admin, or an admin password, either is fine.
	recorder = web.request("POST", path(sam, "role"), "role=admin", session)
	assert.Equal(t, 303, recorder.Code)
	recorder = web.request("POST", path(anna, "role"), "role=read-only", session)
	assert.Equal(t, 303, recorder.Code)
	stored, _ := web.arena.Database.GetUserById(anna.Id)
	assert.Equal(t, model.RoleReadOnly, stored.Role)
	// Anna's own session lost its rights with her role, at once.
	assert.Equal(t, 403, web.request("POST", path(sam, "role"), "role=scorer", session).Code)

	session = loginAs(t, web, "sam", "password-of-sam")
	assert.Equal(t, 400, web.request("POST", path(sam, "delete"), "", session).Code)
	web.arena.EventSettings.AdminPassword = "the admin password"
	admin := loginAs(t, web, "admin", "the admin password")
	assert.Equal(t, 303, web.request("POST", path(sam, "delete"), "", admin).Code)
	assert.Equal(t, 303, web.request("POST", path(anna, "delete"), "", admin).Code)
	users, _ = web.arena.Database.GetAllUsers()
	assert.Empty(t, users)

	// Unknown users and roles are refused.
	assert.Equal(t, 400, web.request("POST", "/setup/users/99/delete", "", admin).Code)
	assert.Equal(t, 400, web.request("POST", "/setup/users/x/role", "role=admin", admin).Code)
	rex, _ := web.arena.Database.CreateUser("rex", "password-of-rex", model.RoleReferee)
	recorder = web.request("POST", path(rex, "role"), "role=owner", admin)
	assert.Equal(t, 400, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "is not a role")
}

func TestUsersPagePassword(t *testing.T) {
	web := setupTestWeb(t)
	web.arena.EventSettings.AdminPassword = "the admin password"
	sam, err := web.arena.Database.CreateUser("sam", "password-of-sam", model.RoleScorer)
	require.Nil(t, err)
	admin := loginAs(t, web, "admin", "the admin password")
	scorer := loginAs(t, web, "sam", "password-of-sam")
	assert.Equal(t, 200, web.request("GET", "/panels/scoring/red_near", "", scorer).Code)

	passwordPath := fmt.Sprintf("/setup/users/%d/password", sam.Id)
	recorder := web.request("POST", passwordPath, "password=short", admin)
	assert.Equal(t, 400, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "a password must be at least 8 characters long")
	assert.Equal(t, 403, web.request("POST", passwordPath, "password=a-new-password", scorer).Code)

	// A new password logs the user out everywhere; the old one no longer logs in.
	recorder = web.request("POST", passwordPath, "password=a-new-password", admin)
	assert.Equal(t, 303, recorder.Code)
	assert.Equal(t, 307, web.request("GET", "/panels/scoring/red_near", "", scorer).Code)
	recorder = web.postHttpResponse("/login", "username=sam&password=password-of-sam")
	assert.Contains(t, recorder.Body.String(), "Invalid login credentials.")
	loginAs(t, web, "sam", "a-new-password")
}

func TestUsersPageTokens(t *testing.T) {
	web := setupTestWeb(t)
	web.arena.EventSettings.AdminPassword = "the admin password"
	admin := loginAs(t, web, "admin", "the admin password")

	// The token is on the answer to its creation and nowhere afterwards. Its name is shown as text, not as HTML.
	recorder := web.request("POST", "/setup/tokens", "name=%3Cb%3EOverlay%3C%2Fb%3E&role=read-only", admin)
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	body := recorder.Body.String()
	token := regexp.MustCompile(`gfms_[A-Za-z0-9_-]{43}`).FindString(body)
	require.NotEmpty(t, token)
	assert.Contains(t, body, "shown only this once")
	assert.Contains(t, body, "&lt;b&gt;Overlay&lt;/b&gt;")
	assert.NotContains(t, body, "<b>Overlay</b>")
	assert.Equal(t, 200, web.request("GET", "/api/v1/rankings", "", bearer(token)).Code)

	body = web.request("GET", "/setup/users", "", admin).Body.String()
	assert.NotContains(t, body, token)
	assert.Contains(t, body, token[:11]+"&hellip;")
	assert.Contains(t, body, "&lt;b&gt;Overlay&lt;/b&gt;")

	for requestBody, message := range map[string]string{
		"name=&role=read-only": "a token needs a name", "name=Robot&role=owner": "is not a role",
	} {
		recorder = web.request("POST", "/setup/tokens", requestBody, admin)
		assert.Equal(t, 400, recorder.Code, requestBody)
		assert.Contains(t, recorder.Body.String(), message)
	}

	// Only an admin makes or revokes tokens; a token cannot make another.
	assert.Equal(t, 403, web.request("POST", "/setup/tokens", "name=More&role=admin", bearer(token)).Code)
	tokens, _ := web.arena.Database.GetAllApiTokens()
	require.Len(t, tokens, 1)
	revoke := fmt.Sprintf("/setup/tokens/%d/delete", tokens[0].Id)
	assert.Equal(t, 307, web.postHttpResponse(revoke, "").Code)
	assert.Equal(t, 303, web.request("POST", revoke, "", admin).Code)
	assert.Equal(t, 401, web.request("GET", "/api/v1/rankings", "", bearer(token)).Code)
	assert.Equal(t, 400, web.request("POST", "/setup/tokens/x/delete", "", admin).Code)
}

func TestSettingsKeepAnAdmin(t *testing.T) {
	web := setupTestWeb(t)
	web.arena.EventSettings.AdminPassword = "the admin password"
	assert.Nil(t, web.arena.Database.UpdateEventSettings(web.arena.EventSettings))
	_, err := web.arena.Database.CreateUser("sam", "password-of-sam", model.RoleScorer)
	require.Nil(t, err)
	admin := loginAs(t, web, "admin", "the admin password")

	// Clearing the admin password would leave a scorer and nobody to manage anything.
	recorder := web.request("POST", "/setup/settings", "name=Event&adminPassword=", admin)
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "The admin password cannot be cleared")
	assert.Equal(t, "the admin password", web.arena.EventSettings.AdminPassword)

	// With an admin user it can be cleared, and logins stay on.
	_, err = web.arena.Database.CreateUser("anna", "password-of-anna", model.RoleAdmin)
	require.Nil(t, err)
	recorder = web.request("POST", "/setup/settings", "name=Event&adminPassword=", admin)
	assert.Equal(t, 303, recorder.Code)
	assert.Equal(t, "", web.arena.EventSettings.AdminPassword)
	assert.Equal(t, 307, web.getHttpResponse("/setup/settings").Code)
	loginAs(t, web, "anna", "password-of-anna")
}
