// Promote an SU account to staff or admin, by email.
//
// Usage: go run cmd/createsustaff/main.go <email> [student|staff|admin]
//
// This exists because there was no other way in, and unlike its Club Fair
// counterpart that was an oversight rather than a design.
//
// `RequireSUStaff` gates seven route groups — every /su-server/admin endpoint,
// the stats dashboard's four, and the three user-management routes — behind
// `users.user_type` being "staff" or "admin". Nothing in the server could
// produce such a row. `model.UserTypeStaff` and `UserTypeAdmin` were declared
// and never assigned anywhere; the only writer is OAuthService.ExchangeCode,
// which hardcodes UserTypeStudent for every Google sign-in; no migration seeds
// one. So the guard was closed against everybody, including the people it was
// written for, and the only way past it was editing the table by hand.
//
// `cmd/createadmin` does not cover this: it writes to `wbw_user`, which is
// Walk-Bike-Week's table with its own roles and its own token. Neither does
// `cmd/createclubfairstaff`, which writes `clubfair_users`. Three products,
// three identity tables, three bootstraps — and this was the missing one.
//
// **The account has to exist first.** This promotes; it does not create. The
// same argument as createclubfairstaff: `oauth_subject` is NOT NULL and is the
// key Google sign-in joins on, so a row invented here is one the real sign-in
// would never find. The person would end up with two accounts and the wrong one
// would be staff. Have them sign in once, then run this.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"su-server/config"
	"su-server/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load()

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: createsustaff <email> [student|staff|admin]")
		os.Exit(1)
	}

	email := strings.TrimSpace(strings.ToLower(os.Args[1]))

	// Defaults to staff, not admin, and that differs from createclubfairstaff
	// on purpose. There is no SU role that only an admin can grant — isStaff
	// treats the two identically — so nothing is locked away by choosing the
	// smaller one, and the common case by far is "let this person read the
	// stats page".
	userType := model.UserTypeStaff
	if len(os.Args) > 2 {
		userType = strings.TrimSpace(strings.ToLower(os.Args[2]))
	}
	switch userType {
	case model.UserTypeStudent, model.UserTypeStaff, model.UserTypeAdmin:
	default:
		fmt.Fprintf(os.Stderr, "user type must be student, staff or admin (got %q)\n", userType)
		os.Exit(1)
	}

	ctx := context.Background()
	pool, err := config.ConnectPool(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "db connect failed:", err)
		os.Exit(1)
	}
	defer pool.Close()

	/*
	   Matched case-insensitively, the same way the repository's own lookups
	   are, so an address copied out of a mail client with a capital in it
	   still finds the row Google created in lower case.

	   The previous value is returned so the output can say what actually
	   changed — running this twice should read as a no-op rather than as a
	   second promotion, and demoting somebody back to student should be just
	   as legible as promoting them.
	*/
	var id int
	var name, previous string
	err = pool.QueryRow(ctx,
		// ⚠ A CTE rather than a subquery inside RETURNING. RETURNING sees the
		// row AFTER the update, and a correlated subquery reading the same
		// table from there is relying on statement-snapshot semantics to hand
		// back the old value — which reads as a clever trick and behaves like
		// one. `before` is evaluated once, up front, and says what it means.
		`WITH before AS (
		   SELECT id, user_type FROM users WHERE lower(email) = lower($1)
		 )
		 UPDATE users u
		    SET user_type = $2, updated_at = now()
		   FROM before b
		  WHERE u.id = b.id
		  RETURNING u.id, u.name, b.user_type`,
		email, userType,
	).Scan(&id, &name, &previous)

	if errors.Is(err, pgx.ErrNoRows) {
		fmt.Fprintf(os.Stderr,
			"no SU account for %s\n"+
				"They have to sign in once at /su-server/auth/google first — this promotes an account, it does not create one.\n",
			email)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "update failed:", err)
		os.Exit(1)
	}

	if previous == userType {
		fmt.Printf("%s (id %d) was already %s — nothing changed\n", name, id, userType)
		return
	}

	fmt.Printf("%s (id %d) is now %s (was %s)\n", name, id, userType, previous)
	fmt.Println("They need to sign in again — user_type is baked into the token, and theirs still says what it did before.")
}
