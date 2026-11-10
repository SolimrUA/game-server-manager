// Package auth establishes who is asking from the token they present, rather than from fields in
// the request. A caller able to invoke the control function directly can put anything in the
// payload; it cannot forge a signature the pool's keys verify.
package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// refetchAfter is how long to wait before fetching the key set again, so a caller sending a key id
// the pool does not publish cannot make every request fetch it.
const refetchAfter = time.Minute

type Claims struct {
	Sub    string
	Groups []string
}

type Verifier struct {
	issuer   string
	audience string
	jwksURL  string
	client   *http.Client

	mu      sync.RWMutex
	keys    map[string]*rsa.PublicKey
	fetched time.Time
}

func NewVerifier(region, userPoolID, clientID string) *Verifier {
	issuer := fmt.Sprintf("https://cognito-idp.%s.amazonaws.com/%s", region, userPoolID)
	return &Verifier{
		issuer:   issuer,
		audience: clientID,
		jwksURL:  issuer + "/.well-known/jwks.json",
		client:   &http.Client{Timeout: 5 * time.Second},
		keys:     map[string]*rsa.PublicKey{},
	}
}

// Verify requires an ID token: an access token carries no audience to bind it to this panel.
func (v *Verifier) Verify(ctx context.Context, token string) (Claims, error) {
	parsed, err := jwt.Parse(token, v.key(ctx),
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired())
	if err != nil {
		return Claims{}, fmt.Errorf("token rejected: %w", err)
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return Claims{}, errors.New("token carries no claims")
	}
	if use, _ := claims["token_use"].(string); use != "id" {
		return Claims{}, fmt.Errorf("token is a %q token, not an id token", use)
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return Claims{}, errors.New("token names no subject")
	}
	return Claims{Sub: sub, Groups: groups(claims)}, nil
}

func groups(claims jwt.MapClaims) []string {
	raw, ok := claims["cognito:groups"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, g := range raw {
		if name, ok := g.(string); ok && name != "" {
			out = append(out, name)
		}
	}
	return out
}

func (v *Verifier) key(ctx context.Context) jwt.Keyfunc {
	return func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("token names no key")
		}
		if key := v.cached(kid); key != nil {
			return key, nil
		}
		if err := v.fetch(ctx); err != nil {
			return nil, err
		}
		if key := v.cached(kid); key != nil {
			return key, nil
		}
		return nil, fmt.Errorf("key %s is not published by the pool", kid)
	}
}

func (v *Verifier) cached(kid string) *rsa.PublicKey {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.keys[kid]
}

func (v *Verifier) fetch(ctx context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if time.Since(v.fetched) < refetchAfter {
		return errors.New("key set was fetched too recently to fetch again")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetching %s answered %s", v.jwksURL, resp.Status)
	}

	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return err
	}

	keys := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" {
			continue
		}
		key, err := rsaKey(k.N, k.E)
		if err != nil {
			return fmt.Errorf("key %s: %w", k.Kid, err)
		}
		keys[k.Kid] = key
	}
	v.keys, v.fetched = keys, time.Now()
	return nil
}

func rsaKey(modulus, exponent string) (*rsa.PublicKey, error) {
	n, err := base64.RawURLEncoding.DecodeString(modulus)
	if err != nil {
		return nil, err
	}
	e, err := base64.RawURLEncoding.DecodeString(exponent)
	if err != nil {
		return nil, err
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}, nil
}
