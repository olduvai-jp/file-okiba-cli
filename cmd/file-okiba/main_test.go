package main

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
	"testing"
)

// Integration-only entry point: macOS uses Keychain rather than SSL_CERT_FILE.
// The distributed binary contains none of this test bootstrap code.
func TestMain(m *testing.M) {
	if os.Getenv("FILE_OKIBA_INTEGRATION_CLI") == "1" {
		pem, e := os.ReadFile(os.Getenv("SSL_CERT_FILE"))
		if e != nil {
			panic(e)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			panic("invalid fixture certificate")
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
		http.DefaultTransport = transport
		os.Args = append([]string{"file-okiba"}, os.Args[1:]...)
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}
