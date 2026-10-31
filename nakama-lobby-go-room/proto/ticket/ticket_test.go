package ticket

import (
	"errors"
	"testing"
	"time"
)

func TestSignVerify(t *testing.T) {
	secret := []byte("s3cret")
	now := time.Unix(1_000_000, 0)
	tok, c, err := Sign(secret, Claims{RoomID: "r1", UserID: "u1", Name: "Alice", Role: RolePlayer, ExpiresAt: now.Add(time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == "" {
		t.Fatal("ID not filled in")
	}
	got, err := Verify(secret, tok, now)
	if err != nil || got != c {
		t.Fatalf("Verify = %+v, %v; want %+v", got, err, c)
	}

	if _, err := Verify([]byte("other"), tok, now); !errors.Is(err, ErrSignature) {
		t.Errorf("wrong secret: %v", err)
	}
	if _, err := Verify(secret, tok, now.Add(time.Minute)); !errors.Is(err, ErrExpired) {
		t.Errorf("expired: %v", err)
	}
	// Flip one character of the payload: the signature no longer matches.
	bad := []byte(tok)
	bad[3] ^= 1
	if _, err := Verify(secret, string(bad), now); err == nil {
		t.Error("tampered ticket accepted")
	}
	for _, s := range []string{"", ".", "abc", "abc.", ".abc"} {
		if _, err := Verify(secret, s, now); !errors.Is(err, ErrMalformed) {
			t.Errorf("Verify(%q) = %v, want ErrMalformed", s, err)
		}
	}
}
