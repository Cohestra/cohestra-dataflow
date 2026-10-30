package api

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
)

// oidcSigningMethods are the asymmetric algorithms accepted for ID tokens.
// HS* is excluded: the client secret must never verify an identity.
var oidcSigningMethods = []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512"}

const (
	jwksMaxAge         = time.Hour
	jwksRefetchBackoff = 30 * time.Second
)

// jwksCache holds each issuer's signing keys by JWKS URI. An unknown kid
// refetches the set (providers rotate keys), at most once per backoff window
// so forged kids cannot turn logins into a request flood against the issuer.
type jwksCache struct {
	mu      sync.Mutex
	entries map[string]*jwksEntry
}

type jwksEntry struct {
	keys    map[string]crypto.PublicKey
	fetched time.Time
}

var oidcJWKS = &jwksCache{entries: map[string]*jwksEntry{}}

func (c *jwksCache) key(ctx context.Context, client *http.Client, uri, kid string) (crypto.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[uri]
	if entry != nil && time.Since(entry.fetched) < jwksMaxAge {
		if key := entry.lookup(kid); key != nil {
			return key, nil
		}
		if time.Since(entry.fetched) < jwksRefetchBackoff {
			return nil, fmt.Errorf("id_token signing key %q not found", kid)
		}
	}
	keys, err := fetchJWKS(ctx, client, uri)
	if err != nil {
		return nil, err
	}
	entry = &jwksEntry{keys: keys, fetched: time.Now()}
	c.entries[uri] = entry
	if key := entry.lookup(kid); key != nil {
		return key, nil
	}
	return nil, fmt.Errorf("id_token signing key %q not found", kid)
}

// lookup finds the key by kid; a token without a kid is accepted only when
// the set has exactly one key, so there is no ambiguity about which one signed.
func (e *jwksEntry) lookup(kid string) crypto.PublicKey {
	if kid == "" && len(e.keys) == 1 {
		for _, key := range e.keys {
			return key
		}
	}
	return e.keys[kid]
}

type jsonWebKey struct {
	Kty, Kid, Use, Crv string
	N, E, X, Y         string
}

func fetchJWKS(ctx context.Context, client *http.Client, uri string) (map[string]crypto.PublicKey, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}
	var set struct {
		Keys []jsonWebKey `json:"keys"`
	}
	if err := doJSON(client, request, &set); err != nil {
		return nil, fmt.Errorf("fetch JWKS: %w", err)
	}
	keys := map[string]crypto.PublicKey{}
	for _, jwk := range set.Keys {
		if jwk.Use != "" && jwk.Use != "sig" {
			continue
		}
		// Keys of unsupported types are skipped so one odd entry does not
		// lock everyone out; a token signed by it fails as an unknown kid.
		if key, err := parseJWK(jwk); err == nil {
			keys[jwk.Kid] = key
		}
	}
	if len(keys) == 0 {
		return nil, errors.New("JWKS has no usable signing keys")
	}
	return keys, nil
}

func parseJWK(jwk jsonWebKey) (crypto.PublicKey, error) {
	switch jwk.Kty {
	case "RSA":
		n, err := base64.RawURLEncoding.DecodeString(jwk.N)
		if err != nil {
			return nil, err
		}
		e, err := base64.RawURLEncoding.DecodeString(jwk.E)
		if err != nil || len(e) == 0 || len(e) > 4 {
			return nil, errors.New("invalid RSA exponent")
		}
		key := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		if key.N.BitLen() < 2048 || key.E < 3 {
			return nil, errors.New("RSA key too weak")
		}
		return key, nil
	case "EC":
		curve := map[string]elliptic.Curve{"P-256": elliptic.P256(), "P-384": elliptic.P384(), "P-521": elliptic.P521()}[jwk.Crv]
		if curve == nil {
			return nil, fmt.Errorf("unsupported curve %q", jwk.Crv)
		}
		x, errX := base64.RawURLEncoding.DecodeString(jwk.X)
		y, errY := base64.RawURLEncoding.DecodeString(jwk.Y)
		size := (curve.Params().BitSize + 7) / 8
		if errX != nil || errY != nil || len(x) != size || len(y) != size {
			return nil, errors.New("invalid EC coordinates")
		}
		// Rejects points that are not on the curve.
		return ecdsa.ParseUncompressedPublicKey(curve, append(append([]byte{4}, x...), y...))
	}
	return nil, fmt.Errorf("unsupported key type %q", jwk.Kty)
}

type oidcIDClaims struct {
	jwt.RegisteredClaims
	Nonce string `json:"nonce"`
	Azp   string `json:"azp"`
}

// verifyOIDCIDToken checks the ID token's signature against the issuer's
// JWKS, then iss, aud (and azp), sub, exp, iat and the login's nonce.
func verifyOIDCIDToken(ctx context.Context, client *http.Client, jwksURI, idToken, issuer, clientID, userinfoSub, nonce string) error {
	if issuer == "" || jwksURI == "" || nonce == "" {
		return errors.New("OIDC verification is not configured")
	}
	claims := &oidcIDClaims{}
	_, err := jwt.NewParser(
		jwt.WithValidMethods(oidcSigningMethods), jwt.WithAudience(clientID), jwt.WithSubject(userinfoSub),
		jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(time.Minute),
	).ParseWithClaims(idToken, claims, func(token *jwt.Token) (interface{}, error) {
		kid, _ := token.Header["kid"].(string)
		return oidcJWKS.key(ctx, client, jwksURI, kid)
	})
	if err != nil {
		return fmt.Errorf("id_token: %w", err)
	}
	if strings.TrimSuffix(claims.Issuer, "/") != issuer {
		return errors.New("id_token issuer mismatch")
	}
	if len(claims.Audience) > 1 && claims.Azp != clientID {
		return errors.New("id_token authorized party mismatch")
	}
	if subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 {
		return errors.New("id_token nonce mismatch")
	}
	return nil
}
