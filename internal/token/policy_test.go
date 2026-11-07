package token

import (
	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestStaffParserPolicy(t *testing.T) {
	for _, purpose := range []string{"access", "refresh"} {
		audience := "hello-local-api"
		if purpose == "refresh" {
			audience = "hello-local-refresh"
		}
		t.Run(purpose, func(t *testing.T) {
			secret := testAccessSecret
			if purpose == "refresh" {
				secret = testRefreshSecret
			}
			cases := []struct {
				name   string
				method jwt.SigningMethod
				change func(jwt.MapClaims)
				secret string
			}{
				{name: "HS384", method: jwt.SigningMethodHS384},
				{name: "HS512", method: jwt.SigningMethodHS512},
				{name: "none", method: jwt.SigningMethodNone},
				{name: "wrong secret", secret: "fixture-wrong-key"},
				{name: "missing issuer", change: func(c jwt.MapClaims) { delete(c, "iss") }},
				{name: "wrong issuer", change: func(c jwt.MapClaims) { c["iss"] = "other-staff" }},
				{name: "missing audience", change: func(c jwt.MapClaims) { delete(c, "aud") }},
				{name: "wrong audience", change: func(c jwt.MapClaims) { c["aud"] = "other-purpose" }},
				{name: "extra audience", change: func(c jwt.MapClaims) { c["aud"] = []string{audience, "other-purpose"} }},
				{name: "overlong lifetime", change: func(c jwt.MapClaims) { c["exp"] = time.Now().Add(48 * time.Hour).Unix() }},
				{name: "future issue time", change: func(c jwt.MapClaims) { c["iat"] = time.Now().Add(time.Minute).Unix() }},
				{name: "missing exp", change: func(c jwt.MapClaims) { delete(c, "exp") }},
				{name: "null exp", change: func(c jwt.MapClaims) { c["exp"] = nil }},
				{name: "invalid exp", change: func(c jwt.MapClaims) { c["exp"] = "tomorrow" }},
				{name: "expired", change: func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Hour).Unix() }},
				{name: "missing identity", change: func(c jwt.MapClaims) { delete(c, "user_id") }},
				{name: "zero identity", change: func(c jwt.MapClaims) { c["user_id"] = 0 }},
				{name: "negative identity", change: func(c jwt.MapClaims) { c["user_id"] = -1 }},
				{name: "fractional identity", change: func(c jwt.MapClaims) { c["user_id"] = 1.5 }},
				{name: "string identity", change: func(c jwt.MapClaims) { c["user_id"] = "7" }},
				{name: "future nbf", change: func(c jwt.MapClaims) { c["nbf"] = time.Now().Add(time.Hour).Unix() }},
				{name: "missing session", change: func(c jwt.MapClaims) { delete(c, "sid") }},
				{name: "invalid jti", change: func(c jwt.MapClaims) { c["jti"] = "bad" }},
				{name: "missing iat", change: func(c jwt.MapClaims) { delete(c, "iat") }},
				{name: "wrong subject", change: func(c jwt.MapClaims) { c["sub"] = "8" }},
				{name: "missing purpose", change: func(c jwt.MapClaims) { delete(c, "type") }},
				{name: "wrong purpose", change: func(c jwt.MapClaims) { c["type"] = "other" }},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					claims := jwt.MapClaims{"user_id": 7, "type": purpose, "iss": "hello-local-staff", "aud": audience, "exp": time.Now().Add(10 * time.Minute).Unix(), "iat": time.Now().Unix(), "sub": "7", "sid": NewFamily().ID.String(), "jti": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}
					if tc.change != nil {
						tc.change(claims)
					}
					method := tc.method
					if method == nil {
						method = jwt.SigningMethodHS256
					}
					var key any = []byte(secret)
					if tc.secret != "" {
						key = []byte(tc.secret)
					}
					if method == jwt.SigningMethodNone {
						key = jwt.UnsafeAllowNoneSignatureType
					}
					candidate := jwt.NewWithClaims(method, claims)
					candidate.Header["kid"] = "dev-" + purpose
					signed, err := candidate.SignedString(key)
					require.NoError(t, err)
					manager := newTestManager()
					if purpose == "access" {
						parsed, err := manager.ParseAccessToken(signed)
						require.ErrorIs(t, err, ErrInvalidToken)
						require.Nil(t, parsed)
					} else {
						parsed, err := manager.ParseRefreshToken(signed)
						require.ErrorIs(t, err, ErrInvalidToken)
						require.Nil(t, parsed)
					}
				})
			}
		})
	}
}

func TestEqualSecretPurposeIsolation(t *testing.T) {
	manager := NewTokenManager("fixture-same-secret", "fixture-same-secret")
	access, err := accessForTest(manager, 7, "VIEWER", "")
	require.NoError(t, err)
	refresh, err := refreshForTest(manager, 7, "VIEWER")
	require.NoError(t, err)
	a, err := manager.ParseAccessToken(refresh)
	require.ErrorIs(t, err, ErrInvalidToken)
	require.Nil(t, a)
	r, err := manager.ParseRefreshToken(access)
	require.ErrorIs(t, err, ErrInvalidToken)
	require.Nil(t, r)
}

func TestParserDoesNotTreatRoleAsAuthority(t *testing.T) {
	manager := newTestManager()
	access, err := accessForTest(manager, 7, "", "")
	require.NoError(t, err)
	claims, err := manager.ParseAccessToken(access)
	require.NoError(t, err)
	require.Equal(t, int64(7), claims.StaffID)
}
