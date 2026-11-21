// Users, roles and API tokens. The admin password in the event settings still logs in as "admin"; users add logins
// with a narrower role, and API tokens let programs act with a role without a login.

package model

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Role is what a login or a token may do.
type Role string

const (
	RoleAdmin    Role = "admin"     // Everything.
	RoleScorer   Role = "scorer"    // Scoring panels and match review edits.
	RoleReferee  Role = "referee"   // The referee panel.
	RoleReadOnly Role = "read-only" // Reports, match review, match logs and every API read; no changes.
)

// Roles lists every role, most powerful first.
var Roles = []Role{RoleAdmin, RoleScorer, RoleReferee, RoleReadOnly}

// AdminUsername is the login of the admin password in the event settings. No user may take it.
const AdminUsername = "admin"

// PasswordIterations is the PBKDF2 work factor for new password hashes, OWASP's 2023 figure for PBKDF2-HMAC-SHA256.
// Tests lower it so that they run quickly; stored hashes carry their own count.
var PasswordIterations = 600000

// Valid reports whether the role is one of Roles.
func (role Role) Valid() bool {
	return slices.Contains(Roles, role)
}

// User is a login with a role.
type User struct {
	Id           int `db:"id"`
	Username     string
	PasswordHash string // pbkdf2-sha256$<iterations>$<salt>$<key>, base64 without padding.
	Role         Role
	CreatedAt    time.Time
}

// ApiToken lets a program use the API with a role. Only a hash of the token is kept; the token itself is shown once.
type ApiToken struct {
	Id         int `db:"id"`
	Name       string
	Role       Role
	TokenHash  string // Hex SHA-256 of the token.
	Prefix     string // The token's first characters, so that a person can tell tokens apart.
	CreatedAt  time.Time
	LastUsedAt time.Time
}

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,40}$`)

// CreateUser stores a new user with the given password.
func (database *Database) CreateUser(username, password string, role Role) (*User, error) {
	if err := database.CheckNewUser(username, role); err != nil {
		return nil, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	return database.CreateUserWithHash(username, hash, role)
}

// CheckNewUser says why a user with the given login and role could not be created, if it could not.
func (database *Database) CheckNewUser(username string, role Role) error {
	if err := database.checkUsername(username, 0); err != nil {
		return err
	}
	if !role.Valid() {
		return fmt.Errorf("%q is not a role", role)
	}
	return nil
}

// CreateUserWithHash stores a new user whose password has already been hashed with HashPassword. It is for callers
// that must not be held up by the hashing, which is slow on purpose: they check with CheckNewUser, hash, and call
// this.
func (database *Database) CreateUserWithHash(username, hash string, role Role) (*User, error) {
	if err := database.CheckNewUser(username, role); err != nil {
		return nil, err
	}
	user := User{Username: username, PasswordHash: hash, Role: role, CreatedAt: time.Now()}
	if err := database.userTable.create(&user); err != nil {
		return nil, err
	}
	return &user, nil
}

// GetAllUsers returns every user, ordered by name.
func (database *Database) GetAllUsers() ([]User, error) {
	users, err := database.userTable.getAll()
	if err != nil {
		return nil, err
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
	return users, nil
}

func (database *Database) GetUserById(id int) (*User, error) {
	return database.userTable.getById(id)
}

// GetUserByUsername returns the user with the given login, or nil if there is none. Logins ignore case.
func (database *Database) GetUserByUsername(username string) (*User, error) {
	users, err := database.userTable.getAll()
	if err != nil {
		return nil, err
	}
	for i := range users {
		if strings.EqualFold(users[i].Username, username) {
			return &users[i], nil
		}
	}
	return nil, nil
}

func (database *Database) UpdateUser(user *User) error {
	if !user.Role.Valid() {
		return fmt.Errorf("%q is not a role", user.Role)
	}
	return database.userTable.update(user)
}

// SetUserPassword replaces a user's password.
func (database *Database) SetUserPassword(user *User, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	user.PasswordHash = hash
	return database.userTable.update(user)
}

// DeleteUser removes a user and logs it out everywhere.
func (database *Database) DeleteUser(id int) error {
	user, err := database.userTable.getById(id)
	if err != nil {
		return err
	}
	if user == nil {
		return fmt.Errorf("there is no user with id %d", id)
	}
	if err = database.DeleteUserSessionsOf(user.Username); err != nil {
		return err
	}
	return database.userTable.delete(id)
}

func (database *Database) TruncateUsers() error {
	return database.userTable.truncate()
}

// checkUsername refuses a login that is malformed, reserved, or taken by a user other than the one with the given id.
func (database *Database) checkUsername(username string, id int) error {
	if !usernamePattern.MatchString(username) {
		return fmt.Errorf("a username is 1 to 40 letters, digits, dots, dashes or underscores")
	}
	if strings.EqualFold(username, AdminUsername) {
		return fmt.Errorf("%q is the login of the admin password in the settings", AdminUsername)
	}
	existing, err := database.GetUserByUsername(username)
	if err != nil {
		return err
	}
	if existing != nil && existing.Id != id {
		return fmt.Errorf("there is already a user named %q", existing.Username)
	}
	return nil
}

// HashPassword returns a salted PBKDF2-HMAC-SHA256 hash of a password, in the form CheckPassword reads.
func HashPassword(password string) (string, error) {
	if len(password) < 8 {
		return "", fmt.Errorf("a password must be at least 8 characters long")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, PasswordIterations, 32)
	if err != nil {
		return "", err
	}
	encoding := base64.RawStdEncoding
	return fmt.Sprintf(
		"pbkdf2-sha256$%d$%s$%s", PasswordIterations, encoding.EncodeToString(salt), encoding.EncodeToString(key),
	), nil
}

// CheckPassword reports whether the password matches a hash made by HashPassword.
func CheckPassword(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1 {
		return false
	}
	encoding := base64.RawStdEncoding
	salt, err := encoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := encoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// apiTokenPrefix starts every API token, so that a token pasted somewhere it should not be is easy to recognise.
const apiTokenPrefix = "gfms_"

// CreateApiToken makes a new token with a role and returns the token, which is not stored and cannot be shown again.
func (database *Database) CreateApiToken(name string, role Role) (*ApiToken, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 60 {
		return nil, "", fmt.Errorf("a token needs a name of at most 60 characters")
	}
	if !role.Valid() {
		return nil, "", fmt.Errorf("%q is not a role", role)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, "", err
	}
	token := apiTokenPrefix + base64.RawURLEncoding.EncodeToString(secret)
	record := ApiToken{
		Name: name, Role: role, TokenHash: hashApiToken(token), Prefix: token[:len(apiTokenPrefix)+6],
		CreatedAt: time.Now(),
	}
	if err := database.apiTokenTable.create(&record); err != nil {
		return nil, "", err
	}
	return &record, token, nil
}

// GetApiTokenByToken returns the stored token matching the given one, or nil if there is none.
func (database *Database) GetApiTokenByToken(token string) (*ApiToken, error) {
	if !strings.HasPrefix(token, apiTokenPrefix) {
		return nil, nil
	}
	tokens, err := database.apiTokenTable.getAll()
	if err != nil {
		return nil, err
	}
	hash := []byte(hashApiToken(token))
	for i := range tokens {
		if subtle.ConstantTimeCompare([]byte(tokens[i].TokenHash), hash) == 1 {
			return &tokens[i], nil
		}
	}
	return nil, nil
}

// GetAllApiTokens returns every token, newest first.
func (database *Database) GetAllApiTokens() ([]ApiToken, error) {
	tokens, err := database.apiTokenTable.getAll()
	if err != nil {
		return nil, err
	}
	sort.Slice(tokens, func(i, j int) bool { return tokens[i].CreatedAt.After(tokens[j].CreatedAt) })
	return tokens, nil
}

func (database *Database) UpdateApiToken(token *ApiToken) error {
	return database.apiTokenTable.update(token)
}

func (database *Database) DeleteApiToken(id int) error {
	return database.apiTokenTable.delete(id)
}

func (database *Database) TruncateApiTokens() error {
	return database.apiTokenTable.truncate()
}

// hashApiToken is the stored form of a token. Tokens are long and random, so a fast hash is enough.
func hashApiToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
