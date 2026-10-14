package load

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"sync"

	"github.com/rootxkit/uspace-lab/internal/oauth"
	"github.com/rootxkit/uspace-lab/internal/runner"
)

// systemsTarget is everything a systems run needs from its targets file,
// read and checked before anything is sent: the endpoints, every
// operator client's credentials, every receiver's keys and the console
// session. Nothing in it is logged or written to the report.
type systemsTarget struct {
	usspBase, tokenURL, audience string
	authBase, pictureURL, origin string
	session                      string
	clients                      []runner.Client
	secrets                      []string
	rx                           []rxCred
	// addresses says how the receivers reach the authority (the report).
	addresses string
}

// checkSystemsTargets refuses a systems targets file that cannot drive
// this tier at all: no load block (it was not written by loadgen seed),
// seeded for another tier, or without the USSP's and the authority's
// endpoints. The fleet itself is compared by systemsCreds.
func checkSystemsTargets(tg *runner.Targets, t *Tier) error {
	if tg.Load == nil {
		return fmt.Errorf("load: the systems targets file has no load block: seed the systems for the tier first (loadgen seed --tier ..., docs/RUNBOOKS/load.md)")
	}
	if tg.Load.Tier != t.Tier {
		return fmt.Errorf("load: the targets file was seeded for tier %q, not %q", tg.Load.Tier, t.Tier)
	}
	if tg.USSP == nil || tg.USSP.BaseURL == "" || tg.USSP.TokenURL == "" || tg.USSP.Audience == "" {
		return fmt.Errorf("load: the targets file names no USSP base URL, token URL and audience")
	}
	if tg.Authority == nil || tg.Authority.BaseURL == "" || tg.Authority.PictureURL == "" || tg.Authority.Origin == "" || tg.Authority.SessionFile == "" {
		return fmt.Errorf("load: the targets file names no authority base URL, picture URL, origin and session file")
	}
	return nil
}

// systemsCreds reads every secret the run needs and compares the fleet
// with the one the systems were seeded for: the digest, the client count
// and the receivers one by one. A difference means the systems would
// refuse the traffic (serials bound to other clients, unknown receivers)
// or judge another fleet, so the run does not start.
func systemsCreds(tg *runner.Targets, fl *Fleet, tier string) (*systemsTarget, error) {
	if fl.paths.Pairs.InnerGapM <= 0 {
		for _, a := range fl.Aircraft {
			if a.Role == RolePair {
				return nil, fmt.Errorf("load: the pairs cross (pairs.inner_gap_m 0): the USSP authorises only one of two intents that share airspace, so a systems run needs an inner gap")
			}
		}
	}
	lt := tg.Load
	if lt == nil || lt.Tier != tier {
		return nil, fmt.Errorf("load: the targets file was not seeded for tier %q", tier)
	}
	if lt.Fleet != fl.Digest() {
		return nil, fmt.Errorf("load: the targets file was seeded for fleet %s, this run lays out %s: seed again (loadgen seed)", lt.Fleet, fl.Digest())
	}
	clients := 0
	for _, a := range fl.Aircraft {
		clients = max(clients, a.Client+1)
	}
	if len(lt.Clients) != clients {
		return nil, fmt.Errorf("load: the targets file has %d operator clients, the fleet %d", len(lt.Clients), clients)
	}
	if len(lt.Receivers) != len(fl.Receivers) {
		return nil, fmt.Errorf("load: the targets file has %d receivers, the fleet %d", len(lt.Receivers), len(fl.Receivers))
	}
	st := &systemsTarget{usspBase: strings.TrimSuffix(tg.USSP.BaseURL, "/"), tokenURL: tg.USSP.TokenURL, audience: tg.USSP.Audience,
		authBase: strings.TrimSuffix(tg.Authority.BaseURL, "/"), pictureURL: tg.Authority.PictureURL, origin: tg.Authority.Origin, clients: lt.Clients}
	var err error
	if st.session, err = tg.Secret(tg.Authority.SessionFile); err != nil {
		return nil, fmt.Errorf("load: the console session: %w", err)
	}
	for i, c := range lt.Clients {
		if c.ClientID == "" {
			return nil, fmt.Errorf("load: operator client %d has no client id", i)
		}
		sec, err := tg.Secret(c.SecretFile)
		if err != nil {
			return nil, fmt.Errorf("load: operator client %d (%s): no secret: %w", i, c.ClientID, err)
		}
		if sec == "" {
			return nil, fmt.Errorf("load: operator client %d (%s): no secret: the file is empty", i, c.ClientID)
		}
		st.secrets = append(st.secrets, sec)
	}
	addrs, err := receiverAddresses(lt.ReceiversForwardedFor, len(lt.Receivers))
	if err != nil {
		return nil, err
	}
	st.addresses = "every receiver from the host's one address (no receivers_forwarded_for): the authority's per-address request budget applies to all of them together"
	if addrs != nil {
		st.addresses = "each receiver under its own address of " + lt.ReceiversForwardedFor + ", named in X-Forwarded-For, which the lab Caddy and the authority trust from the host only (deploy/systems)"
	}
	for i, r := range lt.Receivers {
		if want := fl.ReceiverID(i); r.ID != want {
			return nil, fmt.Errorf("load: receiver %d is %q in the targets file, %q in the fleet", i, r.ID, want)
		}
		bearer, err := tg.Secret(r.BearerKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load: receiver %s: no bearer key: %w", r.ID, err)
		}
		if bearer == "" {
			return nil, fmt.Errorf("load: receiver %s: no bearer key: the file is empty", r.ID)
		}
		hx, err := tg.Secret(r.HMACKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load: receiver %s: %w", r.ID, err)
		}
		key, err := hex.DecodeString(hx)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("load: receiver %s: the HMAC key file does not hold 32 bytes in hex", r.ID)
		}
		c := rxCred{id: r.ID, bearer: bearer, key: key}
		if addrs != nil {
			c.addr = addrs[i]
		}
		st.rx = append(st.rx, c)
	}
	return st, nil
}

// startSystems points the run at the systems: the operator clients'
// tokens from the USSP's token endpoint, the receivers' keys, the
// console session as the authority's picture reads it (the cookie, with
// the console's Origin).
func (r *run) startSystems(_ context.Context, _ *sync.WaitGroup) error {
	s := r.systems
	r.usspBase, r.authBase = s.usspBase, s.authBase
	r.pictureURL = s.pictureURL
	r.pictureHeader = http.Header{"Cookie": {"uspace_session=" + s.session}, "Origin": {s.origin}}
	r.rxCreds = s.rx
	scopes := []string{"ussp.telemetry", "ussp.intents", "ussp.traffic"}
	for i, c := range s.clients {
		r.tokens = append(r.tokens, &oauth.ClientCredentials{TokenURL: s.tokenURL, ClientID: c.ClientID, ClientSecret: s.secrets[i],
			Audience: s.audience, Scopes: scopes})
	}
	return nil
}

// receiverAddresses is the address i+1 of prefix for each of n
// receivers, nil when prefix is empty. Only an IPv4 prefix with room
// for n hosts is taken.
func receiverAddresses(prefix string, n int) ([]string, error) {
	if prefix == "" {
		return nil, nil
	}
	p, err := netip.ParsePrefix(prefix)
	if err != nil || !p.Addr().Is4() {
		return nil, fmt.Errorf("load: receivers_forwarded_for %q is not an IPv4 prefix", prefix)
	}
	if hosts := 1<<(32-p.Bits()) - 2; n > hosts {
		return nil, fmt.Errorf("load: receivers_forwarded_for %s has room for %d receivers, the fleet has %d", prefix, hosts, n)
	}
	out := make([]string, n)
	a := p.Masked().Addr()
	for i := range n {
		a = a.Next()
		out[i] = a.String()
	}
	return out, nil
}
