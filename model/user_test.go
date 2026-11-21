package model

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

// The real work factor makes every hash take a noticeable part of a second.
func init() {
	PasswordIterations = 1000
}

func TestPasswordHashes(t *testing.T) {
	hash, err := HashPassword("correct horse")
	require.Nil(t, err)
	assert.True(t, strings.HasPrefix(hash, "pbkdf2-sha256$1000$"))
	assert.True(t, CheckPassword(hash, "correct horse"))
	assert.False(t, CheckPassword(hash, "correct horsf"))
	assert.False(t, CheckPassword(hash, ""))

	// Each hash has its own salt, and keeps its own work factor when the default changes.
	other, err := HashPassword("correct horse")
	require.Nil(t, err)
	assert.NotEqual(t, hash, other)
	PasswordIterations = 2000
	defer func() { PasswordIterations = 1000 }()
	assert.True(t, CheckPassword(hash, "correct horse"))
	newer, err := HashPassword("correct horse")
	require.Nil(t, err)
	assert.True(t, strings.HasPrefix(newer, "pbkdf2-sha256$2000$"))
	assert.True(t, CheckPassword(newer, "correct horse"))

	_, err = HashPassword("short")
	assert.EqualError(t, err, "a password must be at least 8 characters long")
	for _, malformed := range []string{
		"", "plain", "pbkdf2-sha256$1000$only-three", "md5$1000$c2FsdA$a2V5", "pbkdf2-sha256$0$c2FsdA$a2V5",
		"pbkdf2-sha256$x$c2FsdA$a2V5", "pbkdf2-sha256$1000$!!$a2V5", "pbkdf2-sha256$1000$c2FsdA$!!",
	} {
		assert.False(t, CheckPassword(malformed, "correct horse"), malformed)
	}
}

func TestUsers(t *testing.T) {
	db := setupTestDb(t)

	users, err := db.GetAllUsers()
	assert.Nil(t, err)
	assert.Empty(t, users)

	scorer, err := db.CreateUser("head-scorer", "table by the field", RoleScorer)
	require.Nil(t, err)
	assert.NotEqual(t, "table by the field", scorer.PasswordHash)
	assert.True(t, CheckPassword(scorer.PasswordHash, "table by the field"))
	_, err = db.CreateUser("Anna.B_2", "another password", RoleAdmin)
	require.Nil(t, err)

	// Names are unique whatever their case, "admin" belongs to the settings, and roles are from the list.
	for _, test := range []struct {
		username, password string
		role               Role
		message            string
	}{
		{"Head-Scorer", "some password", RoleScorer, `there is already a user named "head-scorer"`},
		{"admin", "some password", RoleAdmin, `"admin" is the login of the admin password in the settings`},
		{"Admin", "some password", RoleAdmin, `"admin" is the login of the admin password in the settings`},
		{"", "some password", RoleScorer, "a username is 1 to 40 letters, digits, dots, dashes or underscores"},
		{"two words", "some password", RoleScorer, "a username is 1 to 40 letters, digits, dots, dashes or underscores"},
		{"<b>", "some password", RoleScorer, "a username is 1 to 40 letters, digits, dots, dashes or underscores"},
		{"referee", "some password", "owner", `"owner" is not a role`},
		{"referee", "short", RoleReferee, "a password must be at least 8 characters long"},
	} {
		_, err = db.CreateUser(test.username, test.password, test.role)
		assert.EqualError(t, err, test.message, test.username)
	}

	users, _ = db.GetAllUsers()
	if assert.Len(t, users, 2) {
		assert.Equal(t, "Anna.B_2", users[0].Username)
		assert.Equal(t, "head-scorer", users[1].Username)
	}
	found, err := db.GetUserByUsername("HEAD-SCORER")
	assert.Nil(t, err)
	if assert.NotNil(t, found) {
		assert.Equal(t, scorer.Id, found.Id)
	}
	found, _ = db.GetUserByUsername("nobody")
	assert.Nil(t, found)

	// Changing the role and the password.
	scorer.Role = RoleReferee
	assert.Nil(t, db.UpdateUser(scorer))
	scorer.Role = "owner"
	assert.EqualError(t, db.UpdateUser(scorer), `"owner" is not a role`)
	scorer.Role = RoleReferee
	assert.EqualError(t, db.SetUserPassword(scorer, "short"), "a password must be at least 8 characters long")
	assert.Nil(t, db.SetUserPassword(scorer, "a new password"))
	found, _ = db.GetUserById(scorer.Id)
	assert.Equal(t, RoleReferee, found.Role)
	assert.True(t, CheckPassword(found.PasswordHash, "a new password"))
	assert.False(t, CheckPassword(found.PasswordHash, "table by the field"))

	// Deleting a user ends its sessions and nobody else's.
	assert.Nil(t, db.CreateUserSession(&UserSession{Token: "one", Username: "head-scorer"}))
	assert.Nil(t, db.CreateUserSession(&UserSession{Token: "two", Username: "head-scorer"}))
	assert.Nil(t, db.CreateUserSession(&UserSession{Token: "three", Username: "Anna.B_2"}))
	assert.Nil(t, db.DeleteUser(scorer.Id))
	for token, kept := range map[string]bool{"one": false, "two": false, "three": true} {
		session, _ := db.GetUserSessionByToken(token)
		assert.Equal(t, kept, session != nil, token)
	}
	assert.EqualError(t, db.DeleteUser(scorer.Id), "there is no user with id 1")
	users, _ = db.GetAllUsers()
	assert.Len(t, users, 1)
}

func TestApiTokens(t *testing.T) {
	db := setupTestDb(t)

	record, token, err := db.CreateApiToken("  Stream overlay ", RoleReadOnly)
	require.Nil(t, err)
	assert.Equal(t, "Stream overlay", record.Name)
	assert.True(t, strings.HasPrefix(token, "gfms_"))
	assert.Len(t, token, len("gfms_")+43)
	assert.Equal(t, token[:11], record.Prefix)
	// Only a hash is kept.
	assert.Len(t, record.TokenHash, 64)
	assert.NotContains(t, record.TokenHash, token[5:])
	assert.True(t, record.LastUsedAt.IsZero())

	_, other, err := db.CreateApiToken("Scoreboard", RoleScorer)
	require.Nil(t, err)
	assert.NotEqual(t, token, other)

	found, err := db.GetApiTokenByToken(token)
	assert.Nil(t, err)
	if assert.NotNil(t, found) {
		assert.Equal(t, "Stream overlay", found.Name)
		assert.Equal(t, RoleReadOnly, found.Role)
	}
	for _, wrong := range []string{"", "gfms_", token[:len(token)-1], token + "x", strings.ToUpper(token), token[5:]} {
		found, err = db.GetApiTokenByToken(wrong)
		assert.Nil(t, err)
		assert.Nil(t, found, wrong)
	}

	_, _, err = db.CreateApiToken(" ", RoleReadOnly)
	assert.EqualError(t, err, "a token needs a name of at most 60 characters")
	_, _, err = db.CreateApiToken(strings.Repeat("n", 61), RoleReadOnly)
	assert.EqualError(t, err, "a token needs a name of at most 60 characters")
	_, _, err = db.CreateApiToken("Robot", "owner")
	assert.EqualError(t, err, `"owner" is not a role`)

	tokens, _ := db.GetAllApiTokens()
	assert.Len(t, tokens, 2)
	assert.Nil(t, db.DeleteApiToken(record.Id))
	found, _ = db.GetApiTokenByToken(token)
	assert.Nil(t, found)
	found, _ = db.GetApiTokenByToken(other)
	assert.NotNil(t, found)
}

func TestRoles(t *testing.T) {
	for _, role := range Roles {
		assert.True(t, role.Valid(), role)
	}
	assert.False(t, Role("").Valid())
	assert.False(t, Role("Admin").Valid())
}
