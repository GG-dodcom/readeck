// SPDX-FileCopyrightText: © 2024 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/cristalhq/acmd"
	"github.com/doug-martin/goqu/v9"
	"github.com/hlandau/passlib"
	"golang.org/x/term"

	"codeberg.org/readeck/readeck/internal/auth/users"
	"codeberg.org/readeck/readeck/internal/db"
	"codeberg.org/readeck/readeck/pkg/base58"
	"codeberg.org/readeck/readeck/pkg/forms/v2"
)

func init() {
	commands = append(commands, acmd.Command{
		Name:        "user",
		Description: "Create or update a user",
		ExecFunc:    runUser,
	})
}

type userResult struct {
	Exists  bool   `json:"exists"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type userFlags struct {
	appFlags
	User       string
	Password   string
	Email      string
	Group      string
	DryRun     bool
	RemoveTOTP bool
	JSON       bool
}

func (f *userFlags) Flags() *flag.FlagSet {
	fs := f.appFlags.Flags()
	fs.BoolVar(&f.DryRun, "dry-run", false, "do not perform actions")
	fs.BoolVar(&f.DryRun, "n", false, "dry-run (shorthand)")
	fs.BoolVar(&f.JSON, "json", false, "output result in JSON")

	fs.StringVar(&f.Email, "email", "", "email address")
	fs.StringVar(&f.Group, "group", "", "group")
	fs.StringVar(&f.Password, "password", "", strings.TrimSpace(`
password
When password is passed as an argument it can be a clear text, a hashed
password or a mapping to an environment variable.
It will defaults to a prompt for a non existing user.
Examples:
	prompt:          -
	clear text:      abcd
	hashed password: '$5$8hgRGKP8B38VdZwJ$ALKtOJZSZ1AzjVQBwMyBa2gDcmA1swuE0N8HPzmrYP5'
	from env       : env:PASSWORD
`))
	fs.StringVar(&f.Password, "p", "", "password (shorthand)")

	fs.StringVar(&f.User, "user", "", "username")
	fs.StringVar(&f.User, "u", "", "username (shorthand)")
	fs.BoolVar(&f.RemoveTOTP, "remove-totp", false, "remove TOTP for this user")

	return fs
}

func (f *userFlags) setPassword(user *users.User) (err error) {
	if f.DryRun {
		return
	}

	if f.Password == "" && user.ID > 0 {
		return
	}

	rxHashedPassword := regexp.MustCompile(`^\$\w+\$`)
	rxEnvPassword := regexp.MustCompile(`^env:(\w+)$`)

	// Prompt when creating a user without a supplied password
	if user.ID == 0 && f.Password == "" {
		f.Password = "-"
	}

	var hash, password string
	switch password = f.Password; {
	case password == "-":
		// Get password from stdin
		password, err = f.passwordPrompt()
		if err == nil {
			hash, err = user.HashPassword(strings.TrimSpace(password))
		}
	case rxEnvPassword.MatchString(password):
		// Get password from env var
		password, err = f.passwordFromEnv(rxEnvPassword.FindStringSubmatch(password)[1])
	}

	if err != nil {
		return err
	}

	if hash == "" {
		// Hash is empty when the password was supplied directly as a flag
		// or from an env var.
		if rxHashedPassword.MatchString(password) {
			for _, x := range passlib.DefaultSchemes {
				if x.SupportsStub(password) {
					hash = password
					break
				}
			}
			if hash == "" {
				return errors.New("unsupported hash algorithm")
			}
		} else {
			hash, err = user.HashPassword(strings.TrimSpace(password))
		}
	}

	if err != nil {
		return err
	}

	// Set password hash
	user.Password = hash

	// Check if password is empty
	if err := passlib.VerifyNoUpgrade("", user.Password); err == nil {
		return errors.New("password can't be empty")
	}

	// Set or reset seed to logout every instance
	user.SetSeed()

	return nil
}

func (f *userFlags) setGroup(user *users.User) {
	group := user.Group
	if f.Group != "" {
		user.Group = f.Group
	}
	if user.Group == "" {
		user.Group = "user"
	}

	if group != user.Group {
		user.SetSeed()
	}
}

func (f *userFlags) setEmail(user *users.User) {
	email := user.Email
	if f.Email != "" {
		user.Email = f.Email
	}

	// Never leave an empty email address
	if user.Email == "" {
		user.Email = user.Username + "@localhost"
	}

	if email != user.Email {
		user.SetSeed()
	}
}

func (f *userFlags) removeTOTP(user *users.User) {
	if !f.RemoveTOTP || user.ID == 0 {
		return
	}

	user.TOTPSecret = nil
}

func (f *userFlags) passwordPrompt() (string, error) {
	fmt.Print("Enter Password: ")
	p1, err := term.ReadPassword(int(syscall.Stdin))
	println()
	if err != nil {
		return "", err
	}

	fmt.Print("Confirm Password: ")
	p2, err := term.ReadPassword(int(syscall.Stdin))
	println()
	if err != nil {
		return "", err
	}

	if string(p1) != string(p2) {
		return "", errors.New("passwords don't match")
	}

	return string(p1), nil
}

func (f *userFlags) passwordFromEnv(varName string) (string, error) {
	password, ok := os.LookupEnv(varName)
	if !ok {
		return "", fmt.Errorf(`variable "%s" not found`, varName)
	}

	return password, nil
}

func (f *userFlags) output(res *userResult) {
	if f.JSON {
		json.NewEncoder(os.Stdout).Encode(res) //nolint:errcheck
		return
	}
	fmt.Printf("⭐ %s\n", res.Message)
}

func runUser(_ context.Context, args []string) (err error) {
	var flags userFlags
	if err = flags.Flags().Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	flags.User = strings.TrimSpace(flags.User)
	if flags.User == "" {
		return errors.New("-user is empty")
	}

	// Init application
	if err = appPreRun(&flags.appFlags); err != nil {
		return err
	}
	defer appPostRun()

	var user *users.User
	user, err = users.Users.GetOne(goqu.C("username").Eq(flags.User))
	if err != nil {
		if !errors.Is(err, users.ErrNotFound) {
			return err
		}
		err = nil
	}

	if user == nil {
		user = &users.User{
			Username: flags.User,
			Created:  time.Now().UTC(),
			Updated:  time.Now().UTC(),
			UID:      base58.NewUUID(),
		}
	}

	res := &userResult{}

	if user.ID == 0 {
		res.Status = "create"
		res.Message = fmt.Sprintf(`User "%s" successfully created`, user.Username)
	} else {
		res.Exists = true
		res.Status = "update"
		res.Message = fmt.Sprintf(`User "%s" successfully updated`, user.Username)
	}

	if err = flags.setPassword(user); err != nil {
		return err
	}

	flags.setGroup(user)
	flags.setEmail(user)
	flags.removeTOTP(user)

	// Check username and email address
	type userForm struct {
		forms.Form
		Username forms.TextField `json:"username" validate:"trim required_or_nil max_len:128 is_valid_username"`
		Email    forms.TextField `json:"email"    validate:"trim required_or_nil max_len:128 is_valid_email"`
	}
	form := forms.New[userForm](context.Background())
	forms.BindValues(url.Values{"username": {user.Username}, "email": {user.Email}}, form)
	form.IsValid()

	if !form.Username.IsValid() {
		return form.Username.Errors()
	}
	if !form.Email.IsValid() {
		return form.Email.Errors()
	}

	if flags.DryRun {
		flags.output(res)
		return nil
	}

	if user.ID == 0 {
		_, err = db.Q().Insert(db.TableUser).Rows(user).Prepared(true).Executor().Exec()
	} else {
		err = user.Save()
	}

	if err == nil {
		flags.output(res)
	}

	return err
}
