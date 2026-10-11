package auth

import (
	"strings"
	"testing"
)

func TestPasswordHashAndVerify(t *testing.T) {
	h, err := hashPassword("rahasia-desa")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("unexpected hash format %q", h)
	}
	if ok, err := verifyPassword("rahasia-desa", h); err != nil || !ok {
		t.Errorf("correct password: ok=%v err=%v", ok, err)
	}
	if ok, err := verifyPassword("rahasia-kota", h); err != nil || ok {
		t.Errorf("wrong password: ok=%v err=%v", ok, err)
	}
	h2, _ := hashPassword("rahasia-desa")
	if h == h2 {
		t.Error("hashes of the same password must differ (random salt)")
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	for _, bad := range []string{"", "plain", "$argon2i$v=19$m=1,t=1,p=1$c2FsdA$a2V5", "$argon2id$v=18$m=1,t=1,p=1$c2FsdA$a2V5", "$argon2id$v=19$m=x$c2FsdA$a2V5"} {
		if _, err := verifyPassword("x", bad); err == nil {
			t.Errorf("verifyPassword(%q) did not fail", bad)
		}
	}
}
