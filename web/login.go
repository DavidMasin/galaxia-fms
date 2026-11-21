// Copyright 2018 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Web routes for authenticating with the server.

package web

import (
	"crypto/subtle"
	"fmt"
	"github.com/DavidMasin/galaxia-fms/model"
	"github.com/google/uuid"
	"log"
	"net/http"
	"strings"
	"time"
)

// Shows the login form.
func (web *Web) loginHandler(w http.ResponseWriter, r *http.Request) {
	web.renderLogin(w, r, "")
}

// Processes the login request.
func (web *Web) loginPostHandler(w http.ResponseWriter, r *http.Request) {
	username, err := web.checkAuthPassword(r.PostFormValue("username"), r.PostFormValue("password"))
	if err != nil {
		web.renderLogin(w, r, err.Error())
		return
	}

	session := model.UserSession{Token: uuid.New().String(), Username: username, CreatedAt: time.Now()}
	if err := web.arena.Database.CreateUserSession(&session); err != nil {
		handleWebErr(w, err)
		return
	}

	// SameSite keeps other sites from sending requests with the login, and HttpOnly keeps scripts from reading it.
	http.SetCookie(w, &http.Cookie{
		Name: sessionTokenCookie, Value: session.Token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	redirectUrl := r.URL.Query().Get("redirect")
	if redirectUrl == "" || !strings.HasPrefix(redirectUrl, "/") || strings.HasPrefix(redirectUrl, "//") {
		// Only addresses on this server, so that a link cannot use the login page to send someone elsewhere.
		redirectUrl = "/"
	}
	http.Redirect(w, r, redirectUrl, 303)
}

// Ends the login session of the request, if any.
func (web *Web) logoutHandler(w http.ResponseWriter, r *http.Request) {
	if session := web.getUserSessionFromCookie(r); session != nil {
		if err := web.arena.Database.DeleteUserSession(session.Id); err != nil {
			handleWebErr(w, err)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionTokenCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	http.Redirect(w, r, "/", 303)
}

func (web *Web) renderLogin(w http.ResponseWriter, r *http.Request, errorMessage string) {
	template, err := web.parseFiles("templates/login.html", "templates/base.html")
	if err != nil {
		handleWebErr(w, err)
		return
	}
	data := struct {
		*model.EventSettings
		ErrorMessage string
	}{web.arena.EventSettings, errorMessage}
	err = template.ExecuteTemplate(w, "base", data)
	if err != nil {
		handleWebErr(w, err)
		return
	}
}

func (web *Web) getUserSessionFromCookie(r *http.Request) *model.UserSession {
	token, err := r.Cookie(sessionTokenCookie)
	if err != nil {
		return nil
	}
	session, err := web.arena.Database.GetUserSessionByToken(token.Value)
	if err != nil {
		log.Printf("Failed to get user session by token: %v", err)
	}
	return session
}

// checkAuthPassword returns the login to keep in the session, if the password is right: "admin" with the admin
// password from the settings, or a user's name with that user's password.
func (web *Web) checkAuthPassword(username, password string) (string, error) {
	adminPassword := web.arena.EventSettings.AdminPassword
	if username == model.AdminUsername && adminPassword != "" &&
		subtle.ConstantTimeCompare([]byte(password), []byte(adminPassword)) == 1 {
		return model.AdminUsername, nil
	}
	user, err := web.arena.Database.GetUserByUsername(username)
	if err != nil {
		return "", err
	}
	if user != nil && model.CheckPassword(user.PasswordHash, password) {
		return user.Username, nil
	}
	return "", fmt.Errorf("Invalid login credentials.")
}
