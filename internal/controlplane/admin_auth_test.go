package controlplane

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func bcryptHash(t *testing.T, pw string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	return string(h)
}

func TestAdminAuthenticator_Verify(t *testing.T) {
	a := NewAdminAuthenticator("admin", bcryptHash(t, "s3cret"))

	cases := []struct {
		name string
		user string
		pass string
		want bool
	}{
		{"correct", "admin", "s3cret", true},
		{"wrong password", "admin", "nope", false},
		{"wrong username", "root", "s3cret", false},
		{"empty password", "admin", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := a.Verify(c.user, c.pass); got != c.want {
				t.Errorf("Verify(%q,%q) = %v, want %v", c.user, c.pass, got, c.want)
			}
		})
	}
}

func TestAdminAuthenticator_SetHash(t *testing.T) {
	a := NewAdminAuthenticator("admin", bcryptHash(t, "old"))
	if !a.Verify("admin", "old") {
		t.Fatal("old password should verify before rotation")
	}
	a.SetHash(bcryptHash(t, "new"))
	if a.Verify("admin", "old") {
		t.Error("old password still verifies after rotation")
	}
	if !a.Verify("admin", "new") {
		t.Error("new password does not verify after rotation")
	}
}

func TestAdminAuthenticator_UnparseableHash(t *testing.T) {
	a := NewAdminAuthenticator("admin", "not-a-bcrypt-hash")
	if a.Verify("admin", "anything") {
		t.Error("verify must fail when the stored hash is unparseable")
	}
}
