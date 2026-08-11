package service

import "testing"

// The rule that decides whether a student id may open a new account. Worth its
// own test because it is configuration-driven and because getting it wrong in
// either direction is expensive: too strict locks out the students the fair is
// for, too loose lets in everyone it is not.

func TestEligibleIntakeDefaultsTo69(t *testing.T) {
	t.Setenv("CLUBFAIR_INTAKE_PREFIXES", "")

	if !eligibleIntake("6931503029") {
		t.Error("a 69 id must be eligible with no configuration set")
	}
	if eligibleIntake("6831503029") {
		t.Error("a 68 id must not open a new account by default")
	}
}

func TestEligibleIntakeAcceptsSeveralPrefixes(t *testing.T) {
	t.Setenv("CLUBFAIR_INTAKE_PREFIXES", "69, 70")

	for _, id := range []string{"6931503029", "7031503029"} {
		if !eligibleIntake(id) {
			t.Errorf("%s should be eligible for intakes 69,70", id)
		}
	}
	if eligibleIntake("6831503029") {
		t.Error("68 is not in the configured list")
	}
}

func TestEligibleIntakeStarDisablesTheRule(t *testing.T) {
	t.Setenv("CLUBFAIR_INTAKE_PREFIXES", "*")

	for _, id := range []string{"6831503029", "6531503029", ""} {
		if !eligibleIntake(id) {
			t.Errorf("* must admit everything, rejected %q", id)
		}
	}
}

// An id that is not a number at all — a staff address whose local part is a
// name, say — is not eligible to *create* an account, which is the only thing
// this rule governs. Such an account signing in already exists and never
// reaches the check.
func TestEligibleIntakeRejectsNonNumericIds(t *testing.T) {
	t.Setenv("CLUBFAIR_INTAKE_PREFIXES", "69")

	for _, id := range []string{"somebody", "", "6"} {
		if eligibleIntake(id) {
			t.Errorf("%q should not be eligible", id)
		}
	}
}

func TestAllowedIntakeLabel(t *testing.T) {
	t.Setenv("CLUBFAIR_INTAKE_PREFIXES", "69,70")

	if got := AllowedIntakeLabel(); got != "69, 70" {
		t.Errorf("AllowedIntakeLabel() = %q, want %q", got, "69, 70")
	}
}
