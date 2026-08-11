package model

import "testing"

// ProfileComplete is what the app gates on: false sends a student to the rest of
// the sign-up form instead of into the fair. It is one boolean expression, and
// it is tested because the failure it guards against is silent — a flag stuck
// true lets a half-made account straight through, and nothing on screen says so.

func ptr(s string) *string { return &s }

func TestProfileCompleteNeedsEveryField(t *testing.T) {
	hash := "$2a$10$notarealhash"

	complete := ClubFairUser{
		PasswordHash: &hash,
		Phone:        ptr("0683150329"),
		School:       ptr("School of Applied Digital Technology"),
		Major:        ptr("Software Engineering"),
	}
	if !complete.ProfileComplete() {
		t.Fatal("an account with all four fields must be complete")
	}

	// Each field removed in turn. A table rather than four tests because the
	// rule is "all of them", and the interesting part is that no single one is
	// optional.
	cases := map[string]func(u *ClubFairUser){
		"no password": func(u *ClubFairUser) { u.PasswordHash = nil },
		"empty hash":  func(u *ClubFairUser) { empty := ""; u.PasswordHash = &empty },
		"no phone":    func(u *ClubFairUser) { u.Phone = nil },
		"no school":   func(u *ClubFairUser) { u.School = nil },
		"no major":    func(u *ClubFairUser) { u.Major = nil },
		// A column holding a space is not a filled-in field, however the
		// database sees it.
		"blank major": func(u *ClubFairUser) { u.Major = ptr("   ") },
	}
	for name, strip := range cases {
		t.Run(name, func(t *testing.T) {
			user := complete
			strip(&user)
			if user.ProfileComplete() {
				t.Errorf("%s: must not count as a finished sign-up", name)
			}
		})
	}
}

// The state a Google sign-in leaves behind: a real row, and nothing the student
// has typed. This is the case the whole flag exists for.
func TestProfileCompleteFalseForAFreshGoogleAccount(t *testing.T) {
	fresh := ClubFairUser{
		Email:     "6931503029@lamduan.mfu.ac.th",
		StudentID: ptr("6931503029"),
	}
	if fresh.ProfileComplete() {
		t.Error("an account Google has just created has not finished sign-up")
	}
	if NewPublicClubFairUser(fresh).ProfileComplete {
		t.Error("the flag must survive into the response the app reads")
	}
}
