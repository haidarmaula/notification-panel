package token

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testAccessSecret  = "test-access-secret-32-bytes-minimum-x"
	testRefreshSecret = "test-refresh-secret-32-bytes-minimum-x"
)

func newTestManager() *TokenManager {
	return NewTokenManager(testAccessSecret, testRefreshSecret)
}

// --- C9: ParseAccessToken panic regression ---

// TestParseAccessToken_MalformedInput_NoPanic is the regression test for C9.
// Every input in this table previously caused a nil-pointer panic because
// the claims field was dereferenced before the parse error was checked.
func TestParseAccessToken_MalformedInput_NoPanic(t *testing.T) {
	tm := newTestManager()

	cases := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"single segment", "notajwt"},
		{"two segments", "not.a"},
		{"three segments not base64", "not.a.jwt"},
		{"three segments invalid json", "aaaa.bbbb.cccc"},
		{"only dots", "..."},
		{"whitespace", "   "},
		{"long garbage", string(make([]byte, 4096))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims, err := tm.ParseAccessToken(tc.input)
			assert.ErrorIs(t, err, ErrInvalidToken)
			assert.Nil(t, claims)
		})
	}
}

// TestParseAccessToken_WrongSecret verifies that a token signed with a
// different access secret is rejected without panicking.
func TestParseAccessToken_WrongSecret(t *testing.T) {
	issuer := NewTokenManager("secret-A-32-bytes-minimum-xxxxxxxxx", testRefreshSecret)
	verifier := NewTokenManager("secret-B-32-bytes-minimum-xxxxxxxxx", testRefreshSecret)

	tok, err := accessForTest(issuer, 1, "ADMIN", "a@b.com")
	require.NoError(t, err)

	claims, err := verifier.ParseAccessToken(tok)
	assert.ErrorIs(t, err, ErrInvalidToken)
	assert.Nil(t, claims)
}

// TestParseAccessToken_RefreshTokenPresentedAsAccess verifies that a
// refresh token cannot be used as an access token.
func TestParseAccessToken_RefreshTokenPresentedAsAccess(t *testing.T) {
	tm := newTestManager()

	tok, err := refreshForTest(tm, 1, "ADMIN")
	require.NoError(t, err)

	claims, err := tm.ParseAccessToken(tok)
	assert.ErrorIs(t, err, ErrInvalidToken)
	assert.Nil(t, claims)
}

// TestParseAccessToken_ValidToken verifies the happy path for access tokens.
func TestParseAccessToken_ValidToken(t *testing.T) {
	tm := newTestManager()

	tok, err := accessForTest(tm, 42, "SUPER_ADMIN", "admin@example.com")
	require.NoError(t, err)

	claims, err := tm.ParseAccessToken(tok)
	require.NoError(t, err)
	require.NotNil(t, claims)
	assert.Equal(t, int64(42), claims.StaffID)
	assert.Equal(t, "SUPER_ADMIN", claims.RoleName)
	assert.Equal(t, "admin@example.com", claims.Email)
	assert.Equal(t, "access", claims.Type)
}

// --- C10: ParseRefreshToken panic regression ---

// TestParseRefreshToken_MalformedInput_NoPanic is the regression test for
// C10. Every input in this table previously caused a panic because a
// single-value type assertion was applied to a nil Claims field.
func TestParseRefreshToken_MalformedInput_NoPanic(t *testing.T) {
	tm := newTestManager()

	cases := []string{
		"",
		"notajwt",
		"not.a",
		"not.a.jwt",
		"aaaa.bbbb.cccc",
		"...",
		"   ",
		string(make([]byte, 4096)),
	}

	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			claims, err := tm.ParseRefreshToken(in)
			assert.ErrorIs(t, err, ErrInvalidToken)
			assert.Nil(t, claims)
		})
	}
}

// TestParseRefreshToken_WrongSecret verifies that a refresh token signed
// with a different secret is rejected without panicking.
func TestParseRefreshToken_WrongSecret(t *testing.T) {
	issuer := NewTokenManager(testAccessSecret, "secret-A-32-bytes-minimum-xxxxxxxxx")
	verifier := NewTokenManager(testAccessSecret, "secret-B-32-bytes-minimum-xxxxxxxxx")

	tok, err := refreshForTest(issuer, 1, "ADMIN")
	require.NoError(t, err)

	claims, err := verifier.ParseRefreshToken(tok)
	assert.ErrorIs(t, err, ErrInvalidToken)
	assert.Nil(t, claims)
}

// TestParseRefreshToken_AccessTokenPresentedAsRefresh verifies that an
// access token cannot be used as a refresh token.
func TestParseRefreshToken_AccessTokenPresentedAsRefresh(t *testing.T) {
	tm := newTestManager()

	tok, err := accessForTest(tm, 1, "ADMIN", "a@b.com")
	require.NoError(t, err)

	claims, err := tm.ParseRefreshToken(tok)
	assert.ErrorIs(t, err, ErrInvalidToken)
	assert.Nil(t, claims)
}

// TestParseRefreshToken_ValidToken verifies the happy path for refresh
// tokens.
func TestParseRefreshToken_ValidToken(t *testing.T) {
	tm := newTestManager()

	tok, err := refreshForTest(tm, 7, "ADMIN")
	require.NoError(t, err)

	claims, err := tm.ParseRefreshToken(tok)
	require.NoError(t, err)
	require.NotNil(t, claims)
	assert.Equal(t, int64(7), claims.StaffID)
	assert.Equal(t, "ADMIN", claims.RoleName)
	assert.Equal(t, "refresh", claims.Type)
}

func accessForTest(m *TokenManager, staff int64, role, email string) (string, error) {
	p, err := m.Issue(staff, role, email, NewFamily())
	return p.AccessToken, err
}
func refreshForTest(m *TokenManager, staff int64, role string) (string, error) {
	p, err := m.Issue(staff, role, "", NewFamily())
	return p.RefreshToken, err
}
