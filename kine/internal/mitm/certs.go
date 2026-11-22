package mitm

// Root CA + per-domain leaf certificates, mirroring src/mitm/cert/rootCA.js.
// The Root CA is generated once (RSA 2048, 10 years) and lives beside the data
// dir; leaves are minted on demand per SNI and cached in memory by the server.

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"time"
)

const rootCACN = "KineRouter MITM Root CA"

type rootCA struct {
	Key  *rsa.PrivateKey
	Cert *x509.Certificate
}

// EnsureRootCA generates the Root CA when missing or within 30 days of expiry.
func EnsureRootCA(p Paths) error {
	if _, err := os.Stat(p.RootKey); err == nil {
		if _, err := os.Stat(p.RootCert); err == nil {
			if !certExpiringSoon(p.RootCert, 30*24*time.Hour) {
				return nil
			}
		}
	}
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return err
	}
	key, der, err := mintRootCA()
	if err != nil {
		return err
	}
	if err := os.WriteFile(p.RootKey, pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	}), 0o600); err != nil {
		return err
	}
	return os.WriteFile(p.RootCert, pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: der,
	}), 0o644)
}

func mintRootCA() (*rsa.PrivateKey, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	serial, err := randSerial()
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   rootCACN,
			Organization: []string{"KineRouter"},
			Country:      []string{"US"},
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		SubjectKeyId:          keyID(&key.PublicKey),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	return key, der, nil
}

// loadRootCA reads the Root CA pair from disk.
func loadRootCA(p Paths) (*rootCA, error) {
	keyPEM, err := os.ReadFile(p.RootKey)
	if err != nil {
		return nil, fmt.Errorf("Root CA not found: %w", err)
	}
	certPEM, err := os.ReadFile(p.RootCert)
	if err != nil {
		return nil, fmt.Errorf("Root CA not found: %w", err)
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return nil, fmt.Errorf("Root CA key is not PEM")
	}
	key, err := x509.ParsePKCS1PrivateKey(kb.Bytes)
	if err != nil {
		return nil, err
	}
	cb, _ := pem.Decode(certPEM)
	if cb == nil {
		return nil, fmt.Errorf("Root CA cert is not PEM")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, err
	}
	return &rootCA{Key: key, Cert: cert}, nil
}

// LeafFor mints a 1-year leaf for domain (plus its wildcard) signed by the
// Root CA. Called once per SNI — the server caches the result.
func (p Paths) LeafFor(domain string) (*tls.Certificate, error) {
	root, err := loadRootCA(p)
	if err != nil {
		return nil, err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	serial, err := randSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: domain},
		DNSNames:     []string{domain, "*." + domain},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, root.Cert, &key.PublicKey, root.Key)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
	}, nil
}

// certExpiringSoon reports whether the PEM cert at path expires within within.
// Unreadable input counts as expired so it gets regenerated.
func certExpiringSoon(path string, within time.Duration) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return true
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return true
	}
	return cert.NotAfter.Before(time.Now().Add(within))
}

func randSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, limit)
}

func keyID(pub *rsa.PublicKey) []byte {
	return x509.MarshalPKCS1PublicKey(pub)
}
