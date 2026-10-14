package auth_test

import (
	"errors"
	"testing"

	"racket/internal/auth"
	"racket/internal/auth/authtest"
)

func TestDiscordIdentify(t *testing.T) {
	fake := authtest.NewDiscord(t)
	member := auth.Identity{ID: "1", Username: "alice", GlobalName: "Alice", Avatar: "a1"}
	fake.AddUser("member-code", member, true)
	fake.AddUser("outsider-code", auth.Identity{ID: "2", Username: "mallory"}, false)
	d := auth.NewDiscord(fake.Config("http://racket.test/auth/discord/callback"))

	t.Run("member", func(t *testing.T) {
		got, err := d.Identify(t.Context(), "member-code")
		if err != nil {
			t.Fatal(err)
		}
		if got != member {
			t.Errorf("identity = %+v, want %+v", got, member)
		}
	})
	t.Run("not in the server", func(t *testing.T) {
		if _, err := d.Identify(t.Context(), "outsider-code"); !errors.Is(err, auth.ErrNotMember) {
			t.Errorf("err = %v, want ErrNotMember", err)
		}
	})
	t.Run("bad code", func(t *testing.T) {
		_, err := d.Identify(t.Context(), "bogus")
		if err == nil || errors.Is(err, auth.ErrNotMember) {
			t.Errorf("err = %v, want an exchange error", err)
		}
	})
	t.Run("Discord API down", func(t *testing.T) {
		fake.SetFailAPI(true)
		t.Cleanup(func() { fake.SetFailAPI(false) })
		_, err := d.Identify(t.Context(), "member-code")
		if err == nil || errors.Is(err, auth.ErrNotMember) {
			t.Errorf("err = %v, want an API error", err)
		}
	})
}
