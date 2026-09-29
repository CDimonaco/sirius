// Package sipsrv is the SIP half of Sirius. It answers the phone's REGISTER so Sirius
// knows where to ring it, and it places the call.
//
// SIP in one paragraph: it is a text protocol shaped like HTTP, where a request such as
// REGISTER or INVITE gets numeric responses (1xx progress, 2xx success, 4xx refusal).
// A phone announces where it can be reached by sending REGISTER to a registrar, which
// is this file. Later, ringing that phone means sending it an INVITE, which is call.go.
package sipsrv

import (
	"strconv"
	"sync"
	"time"

	"github.com/emiago/diago"
	"github.com/emiago/sipgo/sip"
)

// Expiry bounds. A phone asks how long its registration should last and the registrar
// answers with what it is willing to grant, which is how the phone learns how often to
// refresh. Too short wastes traffic, too long leaves Sirius ringing an address the
// phone has already left.
const (
	MinExpiry     = 60 * time.Second
	MaxExpiry     = 3600 * time.Second
	DefaultExpiry = MaxExpiry
)

// Account is the single credential a phone registers with. One phone per Sirius for
// now, so there is no table of accounts.
type Account struct {
	User     string
	Password string
	Realm    string
}

// Registrar answers REGISTER and remembers where the phone is.
type Registrar struct {
	account Account
	digest  *diago.DigestAuthServer
	now     func() time.Time // injected so expiry can be tested without waiting

	mu       sync.Mutex
	contact  *sip.ContactHeader
	deadline time.Time
}

func NewRegistrar(account Account, now func() time.Time) *Registrar {
	if now == nil {
		now = time.Now
	}
	return &Registrar{account: account, digest: diago.NewDigestServer(), now: now}
}

// Contact reports where to ring, and whether there is anything to ring at all. An
// expired registration counts as nothing.
func (r *Registrar) Contact() (sip.Uri, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.contact == nil || r.now().After(r.deadline) {
		return sip.Uri{}, false
	}
	return r.contact.Address, true
}

// Handle is the sipgo handler. All the thinking happens in Answer so that it can be
// tested without a transport underneath.
func (r *Registrar) Handle(req *sip.Request, tx sip.ServerTransaction) {
	if err := tx.Respond(r.Answer(req)); err != nil {
		// Nothing useful to do: the phone will retry its REGISTER on its own schedule.
		_ = err
	}
}

// Answer decides what to reply to a REGISTER, and records or forgets the contact.
func (r *Registrar) Answer(req *sip.Request) *sip.Response {
	// Challenge first. Without this, any device on the same wifi could register and
	// then receive the audio of your meetings.
	res, err := r.digest.AuthorizeRequest(req, diago.DigestAuth{
		Username: r.account.User,
		Password: r.account.Password,
		Realm:    r.account.Realm,
		Expire:   time.Minute,
	})
	if err != nil || res.StatusCode != int(sip.StatusOK) {
		// AuthorizeRequest already built the right refusal: 401 with a challenge when
		// there were no credentials at all, 401 again when they did not match.
		return res
	}

	contact := req.Contact()
	if contact == nil {
		return sip.NewResponseFromRequest(req, int(sip.StatusBadRequest), "Bad Request", nil)
	}

	granted := grantedExpiry(req, contact)
	ok := sip.NewResponseFromRequest(req, int(sip.StatusOK), "OK", nil)
	ok.AppendHeader(contact.Clone())
	ok.AppendHeader(sip.NewHeader("Expires", strconv.Itoa(int(granted.Seconds()))))

	r.mu.Lock()
	defer r.mu.Unlock()
	if granted == 0 {
		// Expires: 0 is how a phone says it is going away.
		r.contact, r.deadline = nil, time.Time{}
		return ok
	}
	// A later registration replaces the earlier one, which is what happens whenever
	// DHCP moves the phone to a new address.
	r.contact, r.deadline = contact.Clone(), r.now().Add(granted)
	return ok
}

// grantedExpiry reads what the phone asked for and clamps it into what we grant. The
// value can arrive either as an Expires header or as a parameter on the Contact,
// because phones disagree about which one to use.
func grantedExpiry(req *sip.Request, contact *sip.ContactHeader) time.Duration {
	requested := DefaultExpiry
	if v, ok := contact.Params.Get("expires"); ok {
		if secs, err := strconv.Atoi(v); err == nil {
			requested = time.Duration(secs) * time.Second
		}
	}
	if h := req.GetHeader("Expires"); h != nil {
		if secs, err := strconv.Atoi(h.Value()); err == nil {
			requested = time.Duration(secs) * time.Second
		}
	}

	switch {
	case requested == 0:
		return 0 // unregister, not a clamp
	case requested < MinExpiry:
		return MinExpiry
	case requested > MaxExpiry:
		return MaxExpiry
	default:
		return requested
	}
}
