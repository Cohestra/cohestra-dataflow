package api

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
)

const (
	testIssuer   = "https://idp.example.com"
	testClientID = "dataflow-client"
	testSub      = "user-123"
	testNonce    = "nonce-abc"
)

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func rsaJWK(kid string, key *rsa.PublicKey) map[string]string {
	return map[string]string{"kty": "RSA", "kid": kid, "use": "sig", "n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes())}
}

// jwksServer serves whatever key set *keys holds and counts fetches.
func jwksServer(t *testing.T, keys *[]map[string]string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"keys": *keys})
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

func signedIDToken(t *testing.T, method jwt.SigningMethod, kid string, key crypto.PrivateKey, edit func(jwt.MapClaims)) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{"iss": testIssuer, "aud": testClientID, "sub": testSub, "nonce": testNonce, "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix()}
	if edit != nil {
		edit(claims)
	}
	token := jwt.NewWithClaims(method, claims)
	if kid != "" {
		token.Header["kid"] = kid
	}
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func verify(server *httptest.Server, token string) error {
	return verifyOIDCIDToken(context.Background(), server.Client(), server.URL, token, testIssuer, testClientID, testSub, testNonce)
}

func TestVerifyOIDCIDTokenSignatureAndClaims(t *testing.T) {
	signer, _ := rsa.GenerateKey(rand.Reader, 2048)
	attacker, _ := rsa.GenerateKey(rand.Reader, 2048)
	keys := []map[string]string{rsaJWK("k1", &signer.PublicKey)}
	server, _ := jwksServer(t, &keys)

	if err := verify(server, signedIDToken(t, jwt.SigningMethodRS256, "k1", signer, nil)); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	cases := map[string]string{
		"forged signature": signedIDToken(t, jwt.SigningMethodRS256, "k1", attacker, nil),
		"wrong audience":   signedIDToken(t, jwt.SigningMethodRS256, "k1", signer, func(c jwt.MapClaims) { c["aud"] = "other-client" }),
		"wrong issuer":     signedIDToken(t, jwt.SigningMethodRS256, "k1", signer, func(c jwt.MapClaims) { c["iss"] = "https://evil.example.com" }),
		"wrong subject":    signedIDToken(t, jwt.SigningMethodRS256, "k1", signer, func(c jwt.MapClaims) { c["sub"] = "someone-else" }),
		"replayed nonce":   signedIDToken(t, jwt.SigningMethodRS256, "k1", signer, func(c jwt.MapClaims) { c["nonce"] = "old-login" }),
		"missing nonce":    signedIDToken(t, jwt.SigningMethodRS256, "k1", signer, func(c jwt.MapClaims) { delete(c, "nonce") }),
		"expired":          signedIDToken(t, jwt.SigningMethodRS256, "k1", signer, func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Hour).Unix() }),
		"no expiry":        signedIDToken(t, jwt.SigningMethodRS256, "k1", signer, func(c jwt.MapClaims) { delete(c, "exp") }),
		"issued in future": signedIDToken(t, jwt.SigningMethodRS256, "k1", signer, func(c jwt.MapClaims) { c["iat"] = time.Now().Add(time.Hour).Unix() }),
		"foreign azp": signedIDToken(t, jwt.SigningMethodRS256, "k1", signer, func(c jwt.MapClaims) {
			c["aud"] = []string{testClientID, "other-client"}
			c["azp"] = "other-client"
		}),
		// The client secret (or any shared key) must never verify an identity.
		"HMAC": signedIDToken(t, jwt.SigningMethodHS256, "k1", []byte("client-secret"), nil),
		"none": signedIDToken(t, jwt.SigningMethodNone, "k1", jwt.UnsafeAllowNoneSignatureType, nil),
	}
	for name, token := range cases {
		if err := verify(server, token); err == nil {
			t.Errorf("%s: token accepted", name)
		}
	}
	if err := verifyOIDCIDToken(context.Background(), server.Client(), server.URL, signedIDToken(t, jwt.SigningMethodRS256, "k1", signer, nil), testIssuer, testClientID, testSub, ""); err == nil {
		t.Error("token accepted without a login nonce")
	}
}

func TestVerifyOIDCIDTokenECAndKeyRotation(t *testing.T) {
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	size := 32
	pad := func(n *big.Int) string { return b64(n.FillBytes(make([]byte, size))) }
	ecJWK := map[string]string{"kty": "EC", "kid": "ec1", "crv": "P-256", "x": pad(ec.X), "y": pad(ec.Y)}
	keys := []map[string]string{ecJWK}
	server, hits := jwksServer(t, &keys)

	if err := verify(server, signedIDToken(t, jwt.SigningMethodES256, "ec1", ec, nil)); err != nil {
		t.Fatalf("EC token rejected: %v", err)
	}
	// A single-key set also verifies a token that names no kid.
	if err := verify(server, signedIDToken(t, jwt.SigningMethodES256, "", ec, nil)); err != nil {
		t.Fatalf("kid-less token rejected: %v", err)
	}

	// The provider rotates to a new key. Within the backoff window an unknown
	// kid does not refetch; once it has passed, the new key is picked up.
	rotated, _ := rsa.GenerateKey(rand.Reader, 2048)
	keys = []map[string]string{ecJWK, rsaJWK("k2", &rotated.PublicKey)}
	token := signedIDToken(t, jwt.SigningMethodRS256, "k2", rotated, nil)
	before := hits.Load()
	if err := verify(server, token); err == nil || hits.Load() != before {
		t.Fatalf("unknown kid refetched inside the backoff window (err=%v)", err)
	}
	oidcJWKS.mu.Lock()
	oidcJWKS.entries[server.URL].fetched = time.Now().Add(-jwksRefetchBackoff - time.Second)
	oidcJWKS.mu.Unlock()
	if err := verify(server, token); err != nil {
		t.Fatalf("rotated key not picked up: %v", err)
	}
	if hits.Load() != before+1 {
		t.Fatalf("expected one refetch, got %d", hits.Load()-before)
	}
}

func TestParseJWKRejectsWeakOrMalformedKeys(t *testing.T) {
	weak, _ := rsa.GenerateKey(rand.Reader, 1024)
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	offCurve := new(big.Int).Add(ec.Y, big.NewInt(1))
	for name, jwk := range map[string]jsonWebKey{
		"1024-bit RSA":    {Kty: "RSA", N: b64(weak.N.Bytes()), E: b64(big.NewInt(int64(weak.E)).Bytes())},
		"point off curve": {Kty: "EC", Crv: "P-256", X: b64(ec.X.FillBytes(make([]byte, 32))), Y: b64(offCurve.FillBytes(make([]byte, 32)))},
		"unknown curve":   {Kty: "EC", Crv: "secp256k1", X: b64(ec.X.Bytes()), Y: b64(ec.Y.Bytes())},
		"symmetric key":   {Kty: "oct"},
	} {
		if _, err := parseJWK(jwk); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestOIDCDiscoveryDecodesRealDocumentsAndChecksIssuer(t *testing.T) {
	issuer := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"issuer": issuer, "authorization_endpoint": "https://idp/auth", "token_endpoint": "https://idp/token",
			"userinfo_endpoint": "https://idp/userinfo", "jwks_uri": "https://idp/keys",
			"scopes_supported": []string{"openid", "email"}, "claims_supported": []string{"sub"},
		})
	}))
	defer server.Close()
	t.Setenv("OIDC_ISSUER", server.URL+"/")
	s := &Server{HTTP: server.Client()}
	request := httptest.NewRequest(http.MethodGet, "/api/auth/oidc", nil)

	issuer = server.URL
	doc, err := s.oidcDiscovery(request)
	if err != nil || doc.JWKSURI != "https://idp/keys" {
		t.Fatalf("discovery with array fields failed: %+v, %v", doc, err)
	}
	issuer = "https://evil.example.com"
	if _, err := s.oidcDiscovery(request); err == nil || !strings.Contains(err.Error(), "issuer") {
		t.Fatalf("discovery for another issuer accepted: %v", err)
	}
}
