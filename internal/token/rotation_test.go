package token

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type keyFixture struct {
	ID    string `json:"kid"`
	Key   string `json:"key"`
	Until string `json:"verify_until,omitempty"`
}

type ringFixture struct {
	Audience string       `json:"audience"`
	Active   string       `json:"active_kid"`
	Keys     []keyFixture `json:"keys"`
}

type policyFixture struct {
	Issuer  string      `json:"issuer"`
	Access  ringFixture `json:"access"`
	Refresh ringFixture `json:"refresh"`
}

func fixturePolicy() policyFixture {
	key := func(id string, v byte) keyFixture {
		return keyFixture{ID: id, Key: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{v}, 32))}
	}
	return policyFixture{
		Issuer:  "urn:fixture:staff",
		Access:  ringFixture{"urn:fixture:api", "a1", []keyFixture{key("a1", 1)}},
		Refresh: ringFixture{"urn:fixture:renew", "r1", []keyFixture{key("r1", 2)}},
	}
}

func (p policyFixture) manager(t *testing.T) *TokenManager {
	t.Helper()
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	m, err := ParsePolicy(raw, "fixture-api-key")
	require.NoError(t, err)
	return m
}

// The next generation is trusted fleet-wide before any signer switches. Two
// immutable snapshots model different replicas through each rolling restart.
func TestStaffKeyRollover(t *testing.T) {
	p := fixturePolicy()
	first := p.manager(t)
	family := NewFamily()
	oldPair, err := first.Issue(7, "ADMIN", "", family)
	require.NoError(t, err)
	until := time.Now().UTC().Add(25 * time.Hour).Format(time.RFC3339)
	for i, r := range []*ringFixture{&p.Access, &p.Refresh} {
		next := keyFixture{ID: r.Active + "-next", Key: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(i + 3)}, 32)), Until: until}
		r.Keys = append(r.Keys, next)
	}
	prepared := p.manager(t)
	for _, r := range []*ringFixture{&p.Access, &p.Refresh} {
		r.Active = r.Keys[1].ID
		r.Keys[0].Until, r.Keys[1].Until = until, ""
	}
	current := p.manager(t)
	newPair, err := current.Issue(7, "ADMIN", "", family)
	require.NoError(t, err)
	for _, m := range []*TokenManager{prepared, current} {
		for _, pair := range []Pair{oldPair, newPair} {
			a, err := m.ParseAccessToken(pair.AccessToken)
			require.NoError(t, err)
			r, err := m.ParseRefreshToken(pair.RefreshToken)
			require.NoError(t, err)
			require.Equal(t, family.ID.String(), a.SessionID)
			require.Equal(t, family.ID.String(), r.SessionID)
			require.Equal(t, family.ExpiresAt, r.ExpiresAt.Time.UTC())
			require.Equal(t, p.Issuer, a.Issuer)
			require.Equal(t, p.Issuer, r.Issuer)
		}
	}
	for _, r := range []*ringFixture{&p.Access, &p.Refresh} {
		r.Keys[0].Until = time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
	}
	cutoff := p.manager(t)
	for _, r := range []*ringFixture{&p.Access, &p.Refresh} {
		r.Keys = r.Keys[1:]
	}
	removed := p.manager(t)
	for _, m := range []*TokenManager{removed, cutoff} {
		_, err = m.ParseAccessToken(oldPair.AccessToken)
		require.ErrorIs(t, err, ErrInvalidToken)
		_, err = m.ParseRefreshToken(oldPair.RefreshToken)
		require.ErrorIs(t, err, ErrInvalidToken)
		_, err = m.ParseAccessToken(newPair.AccessToken)
		require.NoError(t, err)
		_, err = m.ParseRefreshToken(newPair.RefreshToken)
		require.NoError(t, err)
	}
	_, err = first.ParseAccessToken(newPair.AccessToken)
	require.ErrorIs(t, err, ErrInvalidToken)
	_, err = first.ParseRefreshToken(newPair.RefreshToken)
	require.ErrorIs(t, err, ErrInvalidToken)
}

func TestStaffPolicyRejectsInvalidTrust(t *testing.T) {
	for name, change := range map[string]func(*policyFixture){
		"empty issuer":        func(p *policyFixture) { p.Issuer = "" },
		"whitespace audience": func(p *policyFixture) { p.Access.Audience = " api" },
		"shared audiences":    func(p *policyFixture) { p.Refresh.Audience = p.Access.Audience },
		"shared material":     func(p *policyFixture) { p.Refresh.Keys[0].Key = p.Access.Keys[0].Key },
		"shared key ID":       func(p *policyFixture) { p.Refresh.Keys[0].ID = p.Access.Active; p.Refresh.Active = p.Access.Active },
		"invalid ID":          func(p *policyFixture) { p.Access.Active = "../other" },
		"missing signer":      func(p *policyFixture) { p.Access.Active = "missing"; p.Access.Keys[0].Until = "2030-01-01T00:00:00Z" },
		"active has cutoff":   func(p *policyFixture) { p.Access.Keys[0].Until = "2030-01-01T00:00:00Z" },
		"old key has no cutoff": func(p *policyFixture) {
			p.Access.Keys = append(p.Access.Keys, keyFixture{ID: "a2", Key: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))})
		},
		"non UTC cutoff": func(p *policyFixture) {
			p.Access.Keys = append(p.Access.Keys, keyFixture{ID: "a2", Key: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)), Until: "2030-01-01T02:00:00+02:00"})
		},
		"zero key":         func(p *policyFixture) { p.Access.Keys[0].Key = base64.RawURLEncoding.EncodeToString(make([]byte, 32)) },
		"short key":        func(p *policyFixture) { p.Access.Keys[0].Key = "abc" },
		"noncanonical key": func(p *policyFixture) { p.Access.Keys[0].Key += "=" },
		"too many keys":    func(p *policyFixture) { p.Access.Keys = make([]keyFixture, 9) },
	} {
		t.Run(name, func(t *testing.T) {
			p := fixturePolicy()
			change(&p)
			raw, err := json.Marshal(p)
			require.NoError(t, err)
			m, err := ParsePolicy(raw, "api-fixture")
			require.ErrorIs(t, err, ErrPolicy)
			require.Nil(t, m)
		})
	}
}

// A valid signature does not resolve conflicting interpretations of a JSON name.
func TestStaffRejectsAmbiguousSignedMembers(t *testing.T) {
	manager := newTestManager()
	pair, err := manager.Issue(7, "ADMIN", "", NewFamily())
	require.NoError(t, err)
	for _, purpose := range []string{"access", "refresh"} {
		raw, key := pair.AccessToken, testAccessSecret
		if purpose == "refresh" {
			raw, key = pair.RefreshToken, testRefreshSecret
		}
		parts := strings.Split(raw, ".")
		for _, tc := range []struct {
			name    string
			segment int
			prefix  string
		}{
			{"unsupported critical extension", 0, `"crit":["unsupported"],"unsupported":true,`},
			{"unencoded payload mode", 0, `"b64":false,"crit":["b64"],`},
			{"explicit encoded payload mode disallowed by staff key policy", 0, `"b64":true,"crit":["b64"],`},
			{"duplicate kid", 0, `"kid":"unknown",`},
			{"duplicate subject", 1, `"sub":"8",`},
			{"case-aliased issuer", 1, `"Iss":"other",`},
		} {
			t.Run(purpose+"/"+tc.name, func(t *testing.T) {
				seg, err := base64.RawURLEncoding.DecodeString(parts[tc.segment])
				require.NoError(t, err)
				modified := [2]string{parts[0], parts[1]}
				modified[tc.segment] = base64.RawURLEncoding.EncodeToString([]byte("{" + tc.prefix + string(seg[1:])))
				signed := strings.Join(modified[:], ".")
				mac := hmac.New(sha256.New, []byte(key))
				mac.Write([]byte(signed))
				signed += "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
				if purpose == "access" {
					_, err = manager.ParseAccessToken(signed)
				} else {
					_, err = manager.ParseRefreshToken(signed)
				}
				require.ErrorIs(t, err, ErrInvalidToken)
			})
		}
	}
}
