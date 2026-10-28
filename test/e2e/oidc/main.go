// Command oidc is the HTTPS JWT issuer for authentication E2E: discovery, JWKS
// and a token endpoint, like upstream's test/utils/oidc test server.
//
// Usage: oidc <workdir> <advertise-ip> <audience>
//
// Writes <workdir>/ca.crt, then <workdir>/issuer-url once it is listening.
package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "oidc:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 3 || net.ParseIP(args[1]) == nil {
		return errors.New("usage: oidc <workdir> <advertise-ip> <audience>")
	}
	dir, ip, audience := args[0], net.ParseIP(args[1]), args[2]

	signing, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	cert, err := selfSigned(ip)
	if err != nil {
		return err
	}
	if err := writeFile(dir, "ca.crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})); err != nil {
		return err
	}

	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	issuer := "https://" + net.JoinHostPort(ip.String(), fmt.Sprint(listener.Addr().(*net.TCPAddr).Port))

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer": issuer, "jwks_uri": issuer + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported":              []string{"id_token"},
			"subject_types_supported":               []string{"public"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"keys": []any{map[string]any{
			"kty": "RSA", "kid": "e2e", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(signing.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(signing.E)).Bytes()),
		}}})
	})
	// /token?aud=<audience> issues a token for another audience, which the API
	// server must reject.
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		aud := audience
		if v := r.URL.Query().Get("aud"); v != "" {
			aud = v
		}
		now := time.Now().Unix()
		token, err := sign(signing, map[string]any{"iss": issuer, "aud": aud, "sub": "john_doe", "iat": now, "exp": now + 600})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, token)
	})

	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	secure := tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if err := writeFile(dir, "issuer-url", []byte(issuer+"\n")); err != nil {
		return err
	}
	return server.Serve(secure)
}

func selfSigned(ip net.IP) (tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "authentication e2e"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{ip},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

func sign(key *rsa.PrivateKey, claims map[string]any) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"e2e","typ":"JWT"}`)) +
		"." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		fmt.Fprintln(os.Stderr, "oidc: response:", err)
	}
}

// writeFile replaces name atomically so the script never reads a partial file.
func writeFile(dir, name string, data []byte) error {
	tmp := filepath.Join(dir, "."+name+".tmp")
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, name))
}
