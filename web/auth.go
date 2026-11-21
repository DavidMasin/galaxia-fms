// Who a request comes from, and whether their role lets them do what they ask. A request is identified by its login
// session cookie or by an API token in "Authorization: Bearer <token>".

package web

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/model"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// identity is who a request comes from.
type identity struct {
	Name  string     `json:"name"` // The username, "admin" for the settings' admin password, or the token's name.
	Role  model.Role `json:"role"`
	Token bool       `json:"token"` // Identified by an API token rather than a login.
}

// apiTokenUseInterval limits how often a token's last use is written to the database.
const apiTokenUseInterval = time.Minute

var apiTokenUseMutex sync.Mutex

// authEnabled reports whether logins are needed at all. They are once there is an admin password or a user; without
// either, every page and the whole API are open, as in Cheesy Arena.
func (web *Web) authEnabled() bool {
	if web.arena.EventSettings.AdminPassword != "" {
		return true
	}
	users, err := web.arena.Database.GetAllUsers()
	if err != nil {
		log.Printf("Failed to read the users: %v", err)
		return true
	}
	return len(users) > 0
}

// requestIdentity returns who the request comes from, or nil for nobody (or for a token or session that is no longer
// valid).
func (web *Web) requestIdentity(r *http.Request) *identity {
	if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		record, err := web.arena.Database.GetApiTokenByToken(strings.TrimSpace(token))
		if err != nil {
			log.Printf("Failed to look up an API token: %v", err)
			return nil
		}
		if record == nil {
			return nil
		}
		web.noteApiTokenUse(record)
		return &identity{Name: record.Name, Role: record.Role, Token: true}
	}
	session := web.getUserSessionFromCookie(r)
	if session == nil {
		return nil
	}
	if session.Username == model.AdminUsername {
		// The admin password's login only counts while there is an admin password.
		if web.arena.EventSettings.AdminPassword == "" {
			return nil
		}
		return &identity{Name: model.AdminUsername, Role: model.RoleAdmin}
	}
	user, err := web.arena.Database.GetUserByUsername(session.Username)
	if err != nil {
		log.Printf("Failed to look up user %q: %v", session.Username, err)
		return nil
	}
	if user == nil {
		return nil
	}
	return &identity{Name: user.Username, Role: user.Role}
}

// noteApiTokenUse records when a token was last used, at most once a minute per token.
func (web *Web) noteApiTokenUse(token *model.ApiToken) {
	apiTokenUseMutex.Lock()
	defer apiTokenUseMutex.Unlock()
	now := time.Now()
	if now.Sub(token.LastUsedAt) < apiTokenUseInterval {
		return
	}
	token.LastUsedAt = now
	if err := web.arena.Database.UpdateApiToken(token); err != nil {
		log.Printf("Failed to record the use of API token %q: %v", token.Name, err)
	}
}

// userHasRole lets the request through when logins are off, or when it comes from an admin or from one of the given
// roles. Otherwise it answers the request itself and returns false:
//   - a page asked for by nobody in particular is sent to the login page, which comes back afterwards;
//   - a page asked for by a role that may not have it gets a page saying so, with a way to log in as someone else;
//   - the API gets 401 or 403 with a JSON error.
func (web *Web) userHasRole(w http.ResponseWriter, r *http.Request, roles ...model.Role) bool {
	if !web.authEnabled() {
		return true
	}
	who := web.requestIdentity(r)
	if who != nil && (who.Role == model.RoleAdmin || slices.Contains(roles, who.Role)) {
		return true
	}
	allowed := append([]model.Role{model.RoleAdmin}, roles...)
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if who == nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="galaxia-fms"`)
			writeJsonError(w, http.StatusUnauthorized, "log in, or send an API token as \"Authorization: Bearer <token>\"")
			return false
		}
		writeJsonError(
			w, http.StatusForbidden, fmt.Sprintf("the %s role cannot do this; it needs %s", who.Role, roleList(allowed)),
		)
		return false
	}
	redirect := r.URL.Path
	if r.URL.RawQuery != "" {
		redirect += "?" + r.URL.RawQuery
	}
	if who == nil {
		http.Redirect(w, r, "/login?redirect="+url.QueryEscape(redirect), http.StatusTemporaryRedirect)
		return false
	}
	web.renderForbidden(w, who, allowed, redirect)
	return false
}

// userIsAdmin lets only an admin through (or everyone while logins are off).
func (web *Web) userIsAdmin(w http.ResponseWriter, r *http.Request) bool {
	return web.userHasRole(w, r)
}

// userCanRead lets any role through: for pages and API routes that only show things.
func (web *Web) userCanRead(w http.ResponseWriter, r *http.Request) bool {
	return web.userHasRole(w, r, model.RoleScorer, model.RoleReferee, model.RoleReadOnly)
}

// roleList names roles for a message: "admin or scorer".
func roleList(roles []model.Role) string {
	names := make([]string, len(roles))
	for i, role := range roles {
		names[i] = string(role)
	}
	if len(names) == 1 {
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

var forbiddenTemplate = template.Must(template.New("forbidden").Parse(`<!DOCTYPE html>
<html lang="en" data-bs-theme="dark">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Not allowed - Galaxia FMS</title>
<link href="/static/css/lib/bootstrap.min.css" rel="stylesheet">
</head>
<body>
<div class="container py-5" style="max-width: 36rem;">
  <h3>Not allowed</h3>
  <p>You are logged in as <strong>{{.Name}}</strong>, whose role is <strong>{{.Role}}</strong>. This page needs the
    {{.Allowed}} role.</p>
  <a class="btn btn-primary" href="/login?redirect={{.Redirect}}">Log in as someone else</a>
  <a class="btn btn-outline-secondary" href="/">Home</a>
</div>
</body>
</html>
`))

// renderForbidden tells a logged-in person that their role cannot open the page. The template escapes the address to
// come back to for its place in the link.
func (web *Web) renderForbidden(w http.ResponseWriter, who *identity, allowed []model.Role, redirect string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	data := struct {
		Name, Allowed, Redirect string
		Role                    model.Role
	}{who.Name, roleList(allowed), redirect, who.Role}
	if err := forbiddenTemplate.Execute(w, data); err != nil {
		log.Printf("Failed to render the not-allowed page: %v", err)
	}
}

// apiAuthHandler says whether logins are needed and who the request comes from, so that a program can check its token
// and a page can show who is logged in.
func (web *Web) apiAuthHandler(w http.ResponseWriter, r *http.Request) {
	response := struct {
		Required bool      `json:"required"`
		Identity *identity `json:"identity"`
	}{Required: web.authEnabled(), Identity: web.requestIdentity(r)}
	writeJson(w, http.StatusOK, response)
}
