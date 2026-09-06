package identity

import (
	"testing"
	"time"
)

func TestRightValidate(t *testing.T) {
	if err := (Right{Module: "partners", Entity: "organization", Action: "read"}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Right{
		{Module: "", Entity: "x", Action: "read"},
		{Module: "a b", Entity: "x", Action: "read"},
		{Module: "a", Entity: "x.y", Action: "read"},
	} {
		if err := bad.Validate(); err == nil {
			t.Fatalf("expected error for %+v", bad)
		}
	}
}

func TestCan(t *testing.T) {
	active := User{Status: UserActive}
	grant := []Right{{Module: "sales", Entity: "invoice", Action: "read"}}
	cases := []struct {
		name      string
		user      User
		direct    []Right
		inherited []Right
		module    string
		entity    string
		action    string
		want      bool
	}{
		{"default deny", active, nil, nil, "sales", "invoice", "read", false},
		{"direct grant", active, grant, nil, "sales", "invoice", "read", true},
		{"wrong action", active, grant, nil, "sales", "invoice", "write", false},
		{"wrong entity", active, grant, nil, "sales", "order", "read", false},
		{"inherited grant", active, nil, grant, "sales", "invoice", "read", true},
		{"wildcard entity", active, []Right{{Module: "sales", Entity: "*", Action: "read"}}, nil, "sales", "invoice", "read", true},
		{"admin bypass", User{Status: UserActive, IsAdmin: true}, nil, nil, "sales", "invoice", "delete", true},
		{"disabled admin denied", User{Status: UserDisabled, IsAdmin: true}, nil, nil, "sales", "invoice", "read", false},
		{"locked denied despite grant", User{Status: UserLocked}, grant, nil, "sales", "invoice", "read", false},
	}
	for _, c := range cases {
		if got := Can(c.user, c.direct, c.inherited, c.module, c.entity, c.action); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestPasswordRoundtrip(t *testing.T) {
	hash, err := HashPassword("correct-horse-99")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "correct-horse-99") {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword(hash, "wrong-password-1") {
		t.Fatal("invalid password accepted")
	}
	if VerifyPassword("garbage", "correct-horse-99") {
		t.Fatal("malformed hash accepted")
	}
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
	// Distinct salts produce distinct hashes.
	h2, _ := HashPassword("correct-horse-99")
	if h2 == hash {
		t.Fatal("salt reuse detected")
	}
}

func TestLockoutFlow(t *testing.T) {
	now := time.Now().UTC()
	u := User{Status: UserActive}
	var locked bool
	for i := 0; i < MaxFailedAttempts; i++ {
		u, locked = RegisterFailure(u, now)
	}
	if !locked || u.Status != UserLocked || u.LockedUntil == nil {
		t.Fatalf("expected lockout, got %+v", u)
	}
	if err := LoginAllowed(u, now); err == nil {
		t.Fatal("locked account allowed")
	}
	after := now.Add(LockoutDuration + time.Minute)
	u2, ok := RegisterSuccess(u, after)
	if !ok || u2.Status != UserActive || u2.FailedAttempts != 0 {
		t.Fatalf("expected unlock+reset, got %+v ok=%v", u2, ok)
	}
	disabled := User{Status: UserDisabled}
	if _, ok := RegisterSuccess(disabled, after); ok {
		t.Fatal("disabled account passed success gate")
	}
}
