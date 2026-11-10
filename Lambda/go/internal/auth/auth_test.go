package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testIssuer   = "https://cognito-idp.eu-central-1.amazonaws.com/pool"
	testAudience = "client-id"
	testKid      = "kid-1"
)

func poolWith(t *testing.T, pub *rsa.PublicKey) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kid": testKid,
			"kty": "RSA",
			"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}}})
	}))
}

func verifierFor(t *testing.T, pub *rsa.PublicKey) *Verifier {
	t.Helper()
	pool := poolWith(t, pub)
	t.Cleanup(pool.Close)
	v := NewVerifier("eu-central-1", "pool", testAudience)
	v.issuer, v.jwksURL = testIssuer, pool.URL
	return v
}

func tokenSignedBy(t *testing.T, key *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = testKid
	signed, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return signed
}

func idClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"iss":            testIssuer,
		"aud":            testAudience,
		"token_use":      "id",
		"sub":            "user-1",
		"cognito:groups": []any{"admins", "users"},
		"exp":            time.Now().Add(time.Hour).Unix(),
	}
}

func TestAVerifiedTokenSaysWhoIsAsking(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	got, err := verifierFor(t, &key.PublicKey).Verify(context.Background(),
		tokenSignedBy(t, key, idClaims()))
	if err != nil {
		t.Fatalf("a token the pool signed was rejected: %v", err)
	}
	if got.Sub != "user-1" {
		t.Errorf("subject = %q, want user-1", got.Sub)
	}
	if len(got.Groups) != 2 || got.Groups[0] != "admins" {
		t.Errorf("groups = %v, want [admins users]", got.Groups)
	}
}

func TestATokenThePoolDidNotSignIsRejected(t *testing.T) {
	pool, _ := rsa.GenerateKey(rand.Reader, 2048)
	attacker, _ := rsa.GenerateKey(rand.Reader, 2048)
	// The forged token claims the admin group, which is the escalation being prevented.
	_, err := verifierFor(t, &pool.PublicKey).Verify(context.Background(),
		tokenSignedBy(t, attacker, idClaims()))
	if err == nil {
		t.Fatal("a token signed by another key was accepted")
	}
}

func TestAnUnsignedTokenIsRejected(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, idClaims())
	tok.Header["kid"] = testKid
	unsigned, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("building the unsigned token: %v", err)
	}
	if _, err := verifierFor(t, &key.PublicKey).Verify(context.Background(), unsigned); err == nil {
		t.Fatal("a token with no signature was accepted")
	}
}

func TestATokenForAnotherAudienceIsRejected(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	claims := idClaims()
	claims["aud"] = "someone-elses-client"
	if _, err := verifierFor(t, &key.PublicKey).Verify(context.Background(),
		tokenSignedBy(t, key, claims)); err == nil {
		t.Fatal("a token addressed to another client was accepted")
	}
}

func TestAnAccessTokenIsRejected(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	claims := idClaims()
	claims["token_use"] = "access"
	if _, err := verifierFor(t, &key.PublicKey).Verify(context.Background(),
		tokenSignedBy(t, key, claims)); err == nil {
		t.Fatal("an access token was accepted in place of an id token")
	}
}

func TestAnExpiredTokenIsRejected(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	claims := idClaims()
	claims["exp"] = time.Now().Add(-time.Minute).Unix()
	if _, err := verifierFor(t, &key.PublicKey).Verify(context.Background(),
		tokenSignedBy(t, key, claims)); err == nil {
		t.Fatal("an expired token was accepted")
	}
}
