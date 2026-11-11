// Generates loopback-only WebTransport certificates. Private keys must remain outside Git.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func main() {
	output := flag.String("out", "", "required directory outside the Git repository")
	flag.Parse()
	if *output == "" {
		fmt.Fprintln(os.Stderr, "--out is required; use a private directory outside the repository")
		os.Exit(2)
	}
	if e := generate(*output); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func generate(dir string) error {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return e
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		return e
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "QingYe local WebTransport"}, NotBefore: time.Now().Add(-5 * time.Minute), NotAfter: time.Now().Add(13 * 24 * time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}, DNSNames: []string{"localhost"}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		return e
	}
	keyDER, e := x509.MarshalECPrivateKey(key)
	if e != nil {
		return e
	}
	for _, file := range []struct {
		name, kind string
		data       []byte
	}{{"key.pem", "EC PRIVATE KEY", keyDER}, {"cert.pem", "CERTIFICATE", der}} {
		f, e := os.OpenFile(filepath.Join(dir, file.name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return fmt.Errorf("refusing to replace an existing certificate/key: %w", e)
		}
		e = pem.Encode(f, &pem.Block{Type: file.kind, Bytes: file.data})
		closeErr := f.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	}
	hash := sha256.Sum256(der)
	fmt.Printf("Local certificate created; expires %s; SHA256 %x\n", template.NotAfter.Format(time.RFC3339), hash)
	return nil
}
