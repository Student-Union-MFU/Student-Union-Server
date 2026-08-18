// Promote a Club Fair account to staff or admin, by email.
//
// Usage: go run cmd/createclubfairstaff/main.go <email> [staff|admin]
//
// This exists because there is no other way in, and that is deliberate rather
// than an oversight. Every Club Fair account is created by signing in — with
// Google or with a password — and every one of them is created as a `student`.
// The dashboard's own role editor requires an admin, so with no admins there is
// nothing that can make the first one.
//
// `cmd/createadmin` does not cover this: it writes to `wbw_user`, which is
// Walk-Bike-Week's table. `clubfair_users` is a different table with its own id
// sequence, its own roles and its own token audience — the whole point of
// ClubFairTokenService.
//
// **The account has to exist first.** This promotes; it does not create. Making
// an account here would mean inventing an email address and a name, and the
// email is the key both credential paths join on — a row created with a guessed
// address is a row the real student's Google sign-in will never find, so they
// would end up with two accounts and the stamps on the wrong one. Have the
// person sign in once, then run this.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"su-server/config"
	"su-server/internal/service"

	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load()

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: createclubfairstaff <email> [staff|admin]")
		os.Exit(1)
	}

	email := strings.TrimSpace(strings.ToLower(os.Args[1]))

	// Defaults to admin, because the case this command exists for is the first
	// one — a fair with no admins cannot promote anybody, and a bootstrap that
	// produced a staff account would leave you no better off.
	role := service.ClubFairRoleAdmin
	if len(os.Args) > 2 {
		role = strings.TrimSpace(os.Args[2])
	}
	switch role {
	case service.ClubFairRoleStudent, service.ClubFairRoleStaff, service.ClubFairRoleAdmin:
	default:
		fmt.Fprintf(os.Stderr, "role must be student, staff or admin (got %q)\n", role)
		os.Exit(1)
	}

	ctx := context.Background()
	pool, err := config.ConnectPool(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "db connect failed:", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Matched case-insensitively on email, the same way FindByEmail does, so an
	// address copied out of a mail client with a capital in it still finds the
	// row that Google created in lower case.
	var id int
	var name, previous string
	err = pool.QueryRow(ctx,
		`UPDATE clubfair_users
		    SET role = $2, updated_at = now()
		  WHERE lower(email) = lower($1)
		  RETURNING id, first_name || ' ' || surname, $2`,
		email, role,
	).Scan(&id, &name, &previous)

	if errors.Is(err, pgx.ErrNoRows) {
		fmt.Fprintf(os.Stderr,
			"no Club Fair account for %s\n"+
				"They have to sign in once first — this promotes an account, it does not create one.\n",
			email)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "update failed:", err)
		os.Exit(1)
	}

	fmt.Printf("%s (id %d) is now %s\n", name, id, role)
	fmt.Println("They need to sign in again — the role is baked into the token, and theirs still says what it did before.")
}
