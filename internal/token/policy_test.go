package token

import (
	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestStaffParserPolicy(t *testing.T) {
	for _, purpose := range []string{"access", "refresh"} {
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
				{name: "missing purpose", change: func(c jwt.MapClaims) { delete(c, "type") }},
				{name: "wrong purpose", change: func(c jwt.MapClaims) { c["type"] = "other" }},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					claims := jwt.MapClaims{"user_id": 7, "type": purpose, "exp": time.Now().Add(time.Hour).Unix()}
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
					signed, err := jwt.NewWithClaims(method, claims).SignedString(key)
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
	access, err := manager.GenerateAccessToken(7, "VIEWER", "")
	require.NoError(t, err)
	refresh, err := manager.GenerateRefreshToken(7, "VIEWER")
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
	access, err := manager.GenerateAccessToken(7, "", "")
	require.NoError(t, err)
	claims, err := manager.ParseAccessToken(access)
	require.NoError(t, err)
	require.Equal(t, int64(7), claims.StaffID)
}
