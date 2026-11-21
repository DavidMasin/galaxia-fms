// The Users and API Tokens page: logins with a role, and tokens that let programs use the API with a role.

package web

import (
	"fmt"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/google/uuid"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// usersPage is what the Users and API Tokens page shows.
type usersPage struct {
	*model.EventSettings
	Users         []model.User
	Tokens        []model.ApiToken
	Roles         []model.Role
	AuthEnabled   bool
	Me            *identity
	NewToken      string // A token just created, shown once.
	NewTokenName  string
	Message       string
	ErrorMessage  string
	AdminPassword bool // Whether the settings have an admin password.
}

func (web *Web) usersGetHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	web.renderUsers(w, r, usersPage{Message: r.URL.Query().Get("message")}, http.StatusOK)
}

func (web *Web) renderUsers(w http.ResponseWriter, r *http.Request, page usersPage, status int) {
	var err error
	if page.Users, err = web.arena.Database.GetAllUsers(); err != nil {
		handleWebErr(w, err)
		return
	}
	if page.Tokens, err = web.arena.Database.GetAllApiTokens(); err != nil {
		handleWebErr(w, err)
		return
	}
	page.EventSettings = web.arena.EventSettings
	page.Roles = model.Roles
	page.AuthEnabled = web.authEnabled()
	page.Me = web.requestIdentity(r)
	page.AdminPassword = web.arena.EventSettings.AdminPassword != ""
	template, err := web.parseFiles("templates/setup_users.html", "templates/base.html")
	if err != nil {
		handleWebErr(w, err)
		return
	}
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	if err = template.ExecuteTemplate(w, "base", page); err != nil {
		handleWebErr(w, err)
	}
}

// usersError shows the page again with an error.
func (web *Web) usersError(w http.ResponseWriter, r *http.Request, err error) {
	web.renderUsers(w, r, usersPage{ErrorMessage: err.Error()}, http.StatusBadRequest)
}

// usersDone goes back to the page with a message, so that reloading it does not repeat the change.
func usersDone(w http.ResponseWriter, r *http.Request, message string) {
	http.Redirect(w, r, "/setup/users?message="+url.QueryEscape(message), http.StatusSeeOther)
}

// checkAdminRemains refuses a change that would leave logins needed with nobody able to log in as an admin: no admin
// password in the settings and, among the users the change would leave, no admin.
func checkAdminRemains(adminPassword string, users []model.User) error {
	if adminPassword != "" || len(users) == 0 {
		return nil
	}
	for _, user := range users {
		if user.Role == model.RoleAdmin {
			return nil
		}
	}
	return fmt.Errorf(
		"nobody could log in as an admin afterwards: there is no admin password in the settings, so keep at least " +
			"one user with the admin role",
	)
}

func (web *Web) userCreatePostHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	username := strings.TrimSpace(r.PostFormValue("username"))
	role := model.Role(r.PostFormValue("role"))
	users, err := web.arena.Database.GetAllUsers()
	if err != nil {
		handleWebErr(w, err)
		return
	}
	withNew := append(users, model.User{Username: username, Role: role})
	if err = checkAdminRemains(web.arena.EventSettings.AdminPassword, withNew); err != nil {
		web.usersError(w, r, err)
		return
	}
	wasEnabled := web.authEnabled()
	if err = web.arena.Database.CheckNewUser(username, role); err != nil {
		web.usersError(w, r, err)
		return
	}
	hash, err := web.hashPassword(r.PostFormValue("password"))
	if err != nil {
		web.usersError(w, r, err)
		return
	}
	user, err := web.arena.Database.CreateUserWithHash(username, hash, role)
	if err != nil {
		web.usersError(w, r, err)
		return
	}
	if !wasEnabled {
		// This user turned logins on. Log the person who made it in as it, or the next page would ask them to.
		session := model.UserSession{Token: uuid.New().String(), Username: user.Username, CreatedAt: time.Now()}
		if err = web.arena.Database.CreateUserSession(&session); err != nil {
			handleWebErr(w, err)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name: sessionTokenCookie, Value: session.Token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		})
		usersDone(w, r, fmt.Sprintf("Logins are now on, and you are logged in as %s.", user.Username))
		return
	}
	usersDone(w, r, fmt.Sprintf("Added %s.", user.Username))
}

// userFromRequest returns the user named by the path, answering the request itself if there is none.
func (web *Web) userFromRequest(w http.ResponseWriter, r *http.Request) *model.User {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		web.usersError(w, r, fmt.Errorf("%q is not a user id", r.PathValue("id")))
		return nil
	}
	user, err := web.arena.Database.GetUserById(id)
	if err != nil {
		handleWebErr(w, err)
		return nil
	}
	if user == nil {
		web.usersError(w, r, fmt.Errorf("there is no user with id %d", id))
		return nil
	}
	return user
}

// otherUsers returns every user but the given one, with the given one changed if changed is not nil.
func (web *Web) otherUsers(user *model.User, changed *model.User) ([]model.User, error) {
	users, err := web.arena.Database.GetAllUsers()
	if err != nil {
		return nil, err
	}
	var result []model.User
	for _, other := range users {
		if other.Id != user.Id {
			result = append(result, other)
		}
	}
	if changed != nil {
		result = append(result, *changed)
	}
	return result, nil
}

func (web *Web) userRolePostHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	user := web.userFromRequest(w, r)
	if user == nil {
		return
	}
	changed := *user
	changed.Role = model.Role(r.PostFormValue("role"))
	users, err := web.otherUsers(user, &changed)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	if err = checkAdminRemains(web.arena.EventSettings.AdminPassword, users); err != nil {
		web.usersError(w, r, err)
		return
	}
	if err = web.arena.Database.UpdateUser(&changed); err != nil {
		web.usersError(w, r, err)
		return
	}
	usersDone(w, r, fmt.Sprintf("%s is now %s.", user.Username, changed.Role))
}

func (web *Web) userPasswordPostHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	user := web.userFromRequest(w, r)
	if user == nil {
		return
	}
	hash, err := web.hashPassword(r.PostFormValue("password"))
	if err != nil {
		web.usersError(w, r, err)
		return
	}
	// The user is read again: the arena was let go while the password was hashed.
	if user, err = web.arena.Database.GetUserById(user.Id); err != nil || user == nil {
		web.usersError(w, r, fmt.Errorf("that user is no longer there"))
		return
	}
	user.PasswordHash = hash
	if err = web.arena.Database.UpdateUser(user); err != nil {
		web.usersError(w, r, err)
		return
	}
	// A new password logs the user out everywhere, as a changed admin password does.
	if err := web.arena.Database.DeleteUserSessionsOf(user.Username); err != nil {
		handleWebErr(w, err)
		return
	}
	usersDone(w, r, fmt.Sprintf("Changed the password of %s.", user.Username))
}

func (web *Web) userDeletePostHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	user := web.userFromRequest(w, r)
	if user == nil {
		return
	}
	users, err := web.otherUsers(user, nil)
	if err != nil {
		handleWebErr(w, err)
		return
	}
	if err = checkAdminRemains(web.arena.EventSettings.AdminPassword, users); err != nil {
		web.usersError(w, r, err)
		return
	}
	if err = web.arena.Database.DeleteUser(user.Id); err != nil {
		handleWebErr(w, err)
		return
	}
	usersDone(w, r, fmt.Sprintf("Deleted %s.", user.Username))
}

func (web *Web) tokenCreatePostHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	record, token, err := web.arena.Database.CreateApiToken(
		r.PostFormValue("name"), model.Role(r.PostFormValue("role")),
	)
	if err != nil {
		web.usersError(w, r, err)
		return
	}
	// The token is shown on this answer only; it is not stored and a reload does not show it again.
	w.Header().Set("Cache-Control", "no-store")
	web.renderUsers(w, r, usersPage{NewToken: token, NewTokenName: record.Name}, http.StatusOK)
}

func (web *Web) tokenDeletePostHandler(w http.ResponseWriter, r *http.Request) {
	if !web.userIsAdmin(w, r) {
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		web.usersError(w, r, fmt.Errorf("%q is not a token id", r.PathValue("id")))
		return
	}
	if err = web.arena.Database.DeleteApiToken(id); err != nil {
		handleWebErr(w, err)
		return
	}
	usersDone(w, r, "Revoked the token.")
}

// hashPassword hashes a new password without the arena's lock: the hash is slow to work out on purpose.
func (web *Web) hashPassword(password string) (hash string, err error) {
	web.arena.Unlocked(func() {
		hash, err = model.HashPassword(password)
	})
	return
}
