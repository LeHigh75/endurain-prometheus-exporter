package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/exporter-toolkit/web"
	"golang.org/x/crypto/bcrypt"
)

func testWebCertificate(t *testing.T) (string, string, *x509.CertPool, tls.Certificate) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Exporter test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
	if err := os.WriteFile(certFile, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("invalid certificate")
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile, roots, pair
}

func startTestWeb(t *testing.T, path string, handler http.Handler) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	done := make(chan error, 1)
	go func() { done <- web.Serve(listener, server, &web.FlagConfig{WebConfigFile: &path}, slog.Default()) }()
	t.Cleanup(func() { _ = server.Close(); _ = listener.Close(); <-done })
	return listener.Addr().String()
}

func replaceWebConfig(t *testing.T, path, content string) {
	t.Helper()
	temporary := path + ".new"
	if err := os.WriteFile(temporary, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, path); err != nil {
		t.Fatal(err)
	}
}

func TestToolkitTLSBasicAuthAndReload(t *testing.T) {
	cert, key, roots, _ := testWebCertificate(t)
	file := filepath.Join(t.TempDir(), "web.yml")
	configFor := func(password string) string {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		return "tls_server_config:\n  cert_file: " + cert + "\n  key_file: " + key + "\nbasic_auth_users:\n  prometheus: " + string(hash) + "\n"
	}
	replaceWebConfig(t, file, configFor("demo-first-password"))
	c := testConfig()
	var calls atomic.Int64
	handler := newHandler(c, func(context.Context) (snapshot, error) { calls.Add(1); return snapshot{users: []int64{1}}, nil })
	address := startTestWeb(t, file, handler)
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
	t.Cleanup(client.CloseIdleConnections)
	get := func(path, password string) (int, string) {
		t.Helper()
		req, err := http.NewRequest("GET", "https://"+address+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if password != "" {
			req.SetBasicAuth("prometheus", password)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, string(body)
	}
	for _, path := range []string{"/metrics", "/readyz", "/healthz"} {
		for _, password := range []string{"", "wrong"} {
			if status, _ := get(path, password); status != 401 {
				t.Fatalf("unauthorized request accepted: %d", status)
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatal("toolkit authentication happened after collection")
	}
	if status, body := get("/metrics", "demo-first-password"); status != 200 || !strings.Contains(body, "endurain_up 1") {
		t.Fatal("authenticated HTTPS scrape failed")
	}
	replaceWebConfig(t, file, configFor("demo-second-password"))
	if status, _ := get("/metrics", "demo-first-password"); status != 401 {
		t.Fatal("old password survived configuration reload")
	}
	if status, _ := get("/metrics", "demo-second-password"); status != 200 {
		t.Fatal("new password not loaded")
	}
	before := calls.Load()
	replaceWebConfig(t, file, "basic_auth_users: [invalid-sensitive-value")
	if status, body := get("/metrics", "demo-second-password"); status != 500 || strings.Contains(body, "sensitive") {
		t.Fatal("invalid reloaded configuration failed open or exposed config")
	}
	if calls.Load() != before {
		t.Fatal("invalid configuration reached SQL")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if status, _ := get("/metrics", "demo-second-password"); status != 500 {
		t.Fatal("deleted web configuration failed open")
	}
}

func TestToolkitMutualTLSAndLiveness(t *testing.T) {
	cert, key, roots, pair := testWebCertificate(t)
	file := filepath.Join(t.TempDir(), "web.yml")
	replaceWebConfig(t, file, "tls_server_config:\n  cert_file: "+cert+"\n  key_file: "+key+"\n  client_auth_type: RequireAndVerifyClientCert\n  client_ca_file: "+cert+"\n")
	c := testConfig()
	var calls atomic.Int64
	address := startTestWeb(t, file, newHandler(c, func(context.Context) (snapshot, error) { calls.Add(1); return snapshot{}, nil }))
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	client := &http.Client{Transport: transport, Timeout: time.Second}
	t.Cleanup(client.CloseIdleConnections)
	if response, err := client.Get("https://" + address + "/metrics"); err == nil {
		response.Body.Close()
		t.Fatal("mTLS accepted a missing client certificate")
	}
	if calls.Load() != 0 {
		t.Fatal("unauthenticated mTLS request reached SQL")
	}
	client.CloseIdleConnections()
	transport = transport.Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.Certificates = []tls.Certificate{pair}
	client = &http.Client{Transport: transport, Timeout: time.Second}
	t.Cleanup(client.CloseIdleConnections)
	response, err := client.Get("https://" + address + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 || calls.Load() != 1 {
		t.Fatal("verified client certificate rejected")
	}
	if err := healthcheck(address, file); err != nil {
		t.Fatal("TCP liveness requires TLS credentials")
	}
}

func TestInvalidWebConfigFailsStartup(t *testing.T) {
	c := testConfig()
	c.databaseURL = "postgres://invalid:1/database"
	c.webConfigFile = filepath.Join(t.TempDir(), "missing-secret")
	for _, content := range []string{"", "basic_auth_users: [private-secret", "basic_auth_users:\n  prometheus: not-a-bcrypt-hash\n", "tls_server_config:\n  cert_file: missing.crt\n  key_file: missing.key\n"} {
		if content != "" {
			replaceWebConfig(t, c.webConfigFile, content)
		}
		if err := serve(context.Background(), c); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("unsafe web configuration startup result", err)
		}
	}

}

func TestApplicationServesThroughToolkit(t *testing.T) {
	cert, key, roots, _ := testWebCertificate(t)
	file := filepath.Join(t.TempDir(), "web.yml")
	hash, err := bcrypt.GenerateFromPassword([]byte("demo-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	replaceWebConfig(t, file, "tls_server_config:\n  cert_file: "+cert+"\n  key_file: "+key+"\nbasic_auth_users:\n  prometheus: "+string(hash)+"\n")
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reserved.Addr().String()
	if err := reserved.Close(); err != nil {
		t.Fatal(err)
	}
	c := testConfig()
	c.webConfigFile = file
	c.listen = address
	c.databaseURL = "postgres://test:public-demo-password@127.0.0.1:1/unused?sslmode=disable"
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, c) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("toolkit application did not shut down")
		}
	})
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
	t.Cleanup(client.CloseIdleConnections)
	ready := false
	for range 50 {
		response, err := client.Get("https://" + address + "/healthz")
		if err == nil {
			response.Body.Close()
			if response.StatusCode != 401 {
				t.Fatalf("application bypassed toolkit authentication: %d", response.StatusCode)
			}
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("application did not start with HTTPS")
	}
	req, err := http.NewRequest("GET", "https://"+address+"/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("prometheus", "demo-password")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("application rejected valid toolkit credentials")
	}
}
