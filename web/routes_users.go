// Routes for users, roles and API tokens.

package web

// addUserRoutes registers the Users and API Tokens page.
func (web *Web) addUserRoutes(mux *routeMux) {
	mux.HandleFunc("GET /setup/users", web.usersGetHandler)
	mux.HandleFunc("POST /setup/users", web.userCreatePostHandler)
	mux.HandleFunc("POST /setup/users/{id}/role", web.userRolePostHandler)
	mux.HandleFunc("POST /setup/users/{id}/password", web.userPasswordPostHandler)
	mux.HandleFunc("POST /setup/users/{id}/delete", web.userDeletePostHandler)
	mux.HandleFunc("POST /setup/tokens", web.tokenCreatePostHandler)
	mux.HandleFunc("POST /setup/tokens/{id}/delete", web.tokenDeletePostHandler)
}
