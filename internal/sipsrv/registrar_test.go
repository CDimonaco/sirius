package sipsrv

import (
	"strconv"
	"testing"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/icholy/digest"
)

var testAccount = Account{User: "phone", Password: "secret", Realm: "sirius"}

// clock is a hand-wound clock, so expiry tests take no wall time.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

// register builds a REGISTER the way a phone would, with enough headers for sipgo to
// build a response from it.
func register(contact string, expires int) *sip.Request {
	target := sip.Uri{Scheme: "sip", Host: "sirius.local"}
	req := sip.NewRequest(sip.REGISTER, target)
	req.AppendHeader(&sip.FromHeader{Address: sip.Uri{Scheme: "sip", User: "phone", Host: "sirius.local"}, Params: sip.NewParams()})
	req.AppendHeader(&sip.ToHeader{Address: sip.Uri{Scheme: "sip", User: "phone", Host: "sirius.local"}})
	callID := sip.CallIDHeader("test-call-id")
	req.AppendHeader(&callID)
	req.AppendHeader(&sip.CSeqHeader{SeqNo: 1, MethodName: sip.REGISTER})
	if contact != "" {
		uri := sip.Uri{}
		if err := sip.ParseUri(contact, &uri); err != nil {
			panic(err)
		}
		req.AppendHeader(&sip.ContactHeader{Address: uri, Params: sip.NewParams()})
	}
	if expires >= 0 {
		req.AppendHeader(sip.NewHeader("Expires", strconv.Itoa(expires)))
	}
	return req
}

// authorize answers a challenge the way a phone does: read the nonce, hash the
// password into it, send the same request again with an Authorization header.
func authorize(t *testing.T, req *sip.Request, challenge *sip.Response, password string) *sip.Request {
	t.Helper()

	h := challenge.GetHeader("WWW-Authenticate")
	if h == nil {
		t.Fatalf("response %d carries no challenge", challenge.StatusCode)
	}
	chal, err := digest.ParseChallenge(h.Value())
	if err != nil {
		t.Fatalf("parse challenge: %v", err)
	}
	cred, err := digest.Digest(chal, digest.Options{
		Method:   req.Method.String(),
		URI:      req.Recipient.String(),
		Username: testAccount.User,
		Password: password,
	})
	if err != nil {
		t.Fatalf("build credentials: %v", err)
	}
	signed := req.Clone()
	signed.AppendHeader(sip.NewHeader("Authorization", cred.String()))
	return signed
}

// registered runs the whole two-step handshake and returns the final response.
func registered(t *testing.T, r *Registrar, req *sip.Request, password string) *sip.Response {
	t.Helper()
	challenge := r.Answer(req)
	if challenge.StatusCode != int(sip.StatusUnauthorized) {
		t.Fatalf("first answer = %d, want 401", challenge.StatusCode)
	}
	return r.Answer(authorize(t, req, challenge, password))
}

func TestRegisterWithValidCredentials(t *testing.T) {
	c := &clock{t: time.Now()}
	r := NewRegistrar(testAccount, c.now)

	res := registered(t, r, register("sip:phone@192.168.0.221:5060", 600), testAccount.Password)
	if res.StatusCode != int(sip.StatusOK) {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got := res.GetHeader("Expires").Value(); got != "600" {
		t.Fatalf("granted Expires = %q, want 600", got)
	}
	contact, ok := r.Contact()
	if !ok {
		t.Fatal("no contact recorded")
	}
	if contact.Host != "192.168.0.221" {
		t.Fatalf("contact host = %q, want 192.168.0.221", contact.Host)
	}
}

func TestRegisterWithoutCredentialsIsChallenged(t *testing.T) {
	r := NewRegistrar(testAccount, nil)

	res := r.Answer(register("sip:phone@192.168.0.221:5060", 600))
	if res.StatusCode != int(sip.StatusUnauthorized) {
		t.Fatalf("status = %d, want 401", res.StatusCode)
	}
	if res.GetHeader("WWW-Authenticate") == nil {
		t.Fatal("401 carries no challenge")
	}
	if _, ok := r.Contact(); ok {
		t.Fatal("an unauthenticated REGISTER recorded a contact")
	}
}

func TestRegisterWithWrongPasswordIsRefused(t *testing.T) {
	r := NewRegistrar(testAccount, nil)

	req := register("sip:phone@192.168.0.221:5060", 600)
	res := r.Answer(authorize(t, req, r.Answer(req), "wrong"))
	if res.StatusCode == int(sip.StatusOK) {
		t.Fatal("a wrong password was accepted")
	}
	if _, ok := r.Contact(); ok {
		t.Fatal("a refused REGISTER recorded a contact")
	}
}

func TestRegisterWithoutContactIsRejected(t *testing.T) {
	c := &clock{t: time.Now()}
	r := NewRegistrar(testAccount, c.now)

	registered(t, r, register("sip:phone@192.168.0.221:5060", 600), testAccount.Password)
	res := registered(t, r, register("", 600), testAccount.Password)
	if res.StatusCode != int(sip.StatusBadRequest) {
		t.Fatalf("status = %d, want 400", res.StatusCode)
	}
	if _, ok := r.Contact(); !ok {
		t.Fatal("a rejected REGISTER wiped the earlier contact")
	}
}

func TestExpiryIsClamped(t *testing.T) {
	for _, tc := range []struct{ asked, want int }{
		{asked: 5, want: int(MinExpiry.Seconds())},
		{asked: 600, want: 600},
		{asked: 99999, want: int(MaxExpiry.Seconds())},
		{asked: -1, want: int(DefaultExpiry.Seconds())}, // no Expires header at all
	} {
		r := NewRegistrar(testAccount, nil)
		res := registered(t, r, register("sip:phone@192.168.0.221:5060", tc.asked), testAccount.Password)
		if got := res.GetHeader("Expires").Value(); got != strconv.Itoa(tc.want) {
			t.Errorf("asked %d, granted %s, want %d", tc.asked, got, tc.want)
		}
	}
}

func TestRegistrationLapses(t *testing.T) {
	c := &clock{t: time.Now()}
	r := NewRegistrar(testAccount, c.now)

	registered(t, r, register("sip:phone@192.168.0.221:5060", 60), testAccount.Password)
	c.advance(61 * time.Second)
	if _, ok := r.Contact(); ok {
		t.Fatal("contact still live after its expiry passed")
	}
}

func TestRefreshExtendsRegistration(t *testing.T) {
	c := &clock{t: time.Now()}
	r := NewRegistrar(testAccount, c.now)

	registered(t, r, register("sip:phone@192.168.0.221:5060", 60), testAccount.Password)
	c.advance(30 * time.Second)
	registered(t, r, register("sip:phone@192.168.0.221:5060", 60), testAccount.Password)

	c.advance(45 * time.Second) // 75s after the first, 45s after the second
	if _, ok := r.Contact(); !ok {
		t.Fatal("refreshed registration expired on the original deadline")
	}
}

func TestUnregister(t *testing.T) {
	c := &clock{t: time.Now()}
	r := NewRegistrar(testAccount, c.now)

	registered(t, r, register("sip:phone@192.168.0.221:5060", 600), testAccount.Password)
	res := registered(t, r, register("sip:phone@192.168.0.221:5060", 0), testAccount.Password)
	if res.StatusCode != int(sip.StatusOK) {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if _, ok := r.Contact(); ok {
		t.Fatal("contact survived an Expires: 0")
	}
}

func TestLaterRegistrationReplacesEarlier(t *testing.T) {
	c := &clock{t: time.Now()}
	r := NewRegistrar(testAccount, c.now)

	registered(t, r, register("sip:phone@192.168.0.221:5060", 600), testAccount.Password)
	registered(t, r, register("sip:phone@192.168.0.99:5060", 600), testAccount.Password)

	contact, ok := r.Contact()
	if !ok {
		t.Fatal("no contact recorded")
	}
	if contact.Host != "192.168.0.99" {
		t.Fatalf("contact host = %q, want the newer address", contact.Host)
	}
}
