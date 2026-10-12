// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/zclconf/go-cty/cty"
)

func TestProvisioner_connInfo(t *testing.T) {
	v := cty.ObjectVal(map[string]cty.Value{
		"type":         cty.StringVal("ssh"),
		"user":         cty.StringVal("root"),
		"password":     cty.StringVal("supersecret"),
		"private_key":  cty.StringVal("someprivatekeycontents"),
		"certificate":  cty.StringVal("somecertificate"),
		"host":         cty.StringVal("127.0.0.1"),
		"port":         cty.StringVal("22"),
		"timeout":      cty.StringVal("30s"),
		"bastion_host": cty.StringVal("127.0.1.1"),
		"bastion_port": cty.NumberIntVal(20022),
	})

	conf, err := parseConnectionInfo(v)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	if conf.User != "root" {
		t.Fatalf("bad: %v", conf)
	}
	if conf.Password != "supersecret" {
		t.Fatalf("bad: %v", conf)
	}
	if conf.PrivateKey != "someprivatekeycontents" {
		t.Fatalf("bad: %v", conf)
	}
	if conf.Certificate != "somecertificate" {
		t.Fatalf("bad: %v", conf)
	}
	if conf.Host != "127.0.0.1" {
		t.Fatalf("bad: %v", conf)
	}
	if conf.Port != 22 {
		t.Fatalf("bad: %v", conf)
	}
	if conf.Timeout != "30s" {
		t.Fatalf("bad: %v", conf)
	}
	if conf.ScriptPath != DefaultUnixScriptPath {
		t.Fatalf("bad: %v", conf)
	}
	if conf.TargetPlatform != TargetPlatformUnix {
		t.Fatalf("bad: %v", conf)
	}
	if conf.BastionHost != "127.0.1.1" {
		t.Fatalf("bad: %v", conf)
	}
	if conf.BastionPort != 20022 {
		t.Fatalf("bad: %v", conf)
	}
	if conf.BastionUser != "root" {
		t.Fatalf("bad: %v", conf)
	}
	if conf.BastionPassword != "supersecret" {
		t.Fatalf("bad: %v", conf)
	}
	if conf.BastionPrivateKey != "someprivatekeycontents" {
		t.Fatalf("bad: %v", conf)
	}
}

func TestProvisioner_connInfoIpv6(t *testing.T) {
	v := cty.ObjectVal(map[string]cty.Value{
		"type":         cty.StringVal("ssh"),
		"user":         cty.StringVal("root"),
		"password":     cty.StringVal("supersecret"),
		"private_key":  cty.StringVal("someprivatekeycontents"),
		"host":         cty.StringVal("::1"),
		"port":         cty.StringVal("22"),
		"timeout":      cty.StringVal("30s"),
		"bastion_host": cty.StringVal("::1"),
	})

	conf, err := parseConnectionInfo(v)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	if conf.Host != "[::1]" {
		t.Fatalf("bad: %v", conf)
	}

	if conf.BastionHost != "[::1]" {
		t.Fatalf("bad %v", conf)
	}
}

func TestProvisioner_connInfoHostname(t *testing.T) {
	v := cty.ObjectVal(map[string]cty.Value{
		"type":         cty.StringVal("ssh"),
		"user":         cty.StringVal("root"),
		"password":     cty.StringVal("supersecret"),
		"private_key":  cty.StringVal("someprivatekeycontents"),
		"host":         cty.StringVal("example.com"),
		"port":         cty.StringVal("22"),
		"timeout":      cty.StringVal("30s"),
		"bastion_host": cty.StringVal("example.com"),
	})

	conf, err := parseConnectionInfo(v)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	if conf.Host != "example.com" {
		t.Fatalf("bad: %v", conf)
	}

	if conf.BastionHost != "example.com" {
		t.Fatalf("bad %v", conf)
	}
}

func TestProvisioner_connInfoEmptyHostname(t *testing.T) {
	v := cty.ObjectVal(map[string]cty.Value{
		"type":        cty.StringVal("ssh"),
		"user":        cty.StringVal("root"),
		"password":    cty.StringVal("supersecret"),
		"private_key": cty.StringVal("someprivatekeycontents"),
		"port":        cty.StringVal("22"),
		"timeout":     cty.StringVal("30s"),
	})

	_, err := parseConnectionInfo(v)
	if err == nil {
		t.Fatalf("bad: should not allow empty host")
	}
}

func TestProvisioner_connInfoProxy(t *testing.T) {
	v := cty.ObjectVal(map[string]cty.Value{
		"type":                cty.StringVal("ssh"),
		"user":                cty.StringVal("root"),
		"password":            cty.StringVal("supersecret"),
		"private_key":         cty.StringVal("someprivatekeycontents"),
		"host":                cty.StringVal("example.com"),
		"port":                cty.StringVal("22"),
		"timeout":             cty.StringVal("30s"),
		"proxy_scheme":        cty.StringVal("http"),
		"proxy_host":          cty.StringVal("proxy.example.com"),
		"proxy_port":          cty.StringVal("80"),
		"proxy_user_name":     cty.StringVal("proxyuser"),
		"proxy_user_password": cty.StringVal("proxyuser_password"),
	})

	conf, err := parseConnectionInfo(v)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	if conf.Host != "example.com" {
		t.Fatalf("bad: %v", conf)
	}

	if conf.ProxyScheme != "http" {
		t.Fatalf("bad: %v", conf)
	}

	if conf.ProxyHost != "proxy.example.com" {
		t.Fatalf("bad: %v", conf)
	}

	if conf.ProxyPort != 80 {
		t.Fatalf("bad: %v", conf)
	}

	if conf.ProxyUserName != "proxyuser" {
		t.Fatalf("bad: %v", conf)
	}

	if conf.ProxyUserPassword != "proxyuser_password" {
		t.Fatalf("bad: %v", conf)
	}
}

func TestProvisioner_stringBastionPort(t *testing.T) {
	v := cty.ObjectVal(map[string]cty.Value{
		"type":         cty.StringVal("ssh"),
		"user":         cty.StringVal("root"),
		"password":     cty.StringVal("supersecret"),
		"private_key":  cty.StringVal("someprivatekeycontents"),
		"host":         cty.StringVal("example.com"),
		"port":         cty.StringVal("22"),
		"timeout":      cty.StringVal("30s"),
		"bastion_host": cty.StringVal("example.com"),
		"bastion_port": cty.StringVal("12345"),
	})

	conf, err := parseConnectionInfo(v)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	if conf.BastionPort != 12345 {
		t.Fatalf("bad %v", conf)
	}
}

func TestProvisioner_invalidPortNumber(t *testing.T) {
	v := cty.ObjectVal(map[string]cty.Value{
		"type":        cty.StringVal("ssh"),
		"user":        cty.StringVal("root"),
		"password":    cty.StringVal("supersecret"),
		"private_key": cty.StringVal("someprivatekeycontents"),
		"host":        cty.StringVal("example.com"),
		"port":        cty.NumberIntVal(123456789),
	})

	_, err := parseConnectionInfo(v)
	if err == nil {
		t.Fatalf("bad: should not allow invalid port number")
	}
	if got, want := err.Error(), "value must be a whole number, between 0 and 65535 inclusive"; got != want {
		t.Errorf("unexpected error\n got: %s\nwant: %s", got, want)
	}
}

// TestSignCertWithPrivateKeyErrorRedaction ensures that errors returned by
// signCertWithPrivateKey never include the raw private key or certificate
// material, which are secrets. See https://github.com/opentofu/opentofu/issues/4084.
func TestSignCertWithPrivateKeyErrorRedaction(t *testing.T) {
	// validPrivateKey is generated at runtime because the package's key
	// fixtures live behind //go:build !race (communicator_test.go).
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating private key: %s", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshaling private key: %s", err)
	}
	validPrivateKey := string(pem.EncodeToMemory(block))

	mismatchedPublicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating mismatched certificate key: %s", err)
	}
	certKey, err := ssh.NewPublicKey(mismatchedPublicKey)
	if err != nil {
		t.Fatalf("converting mismatched certificate key: %s", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("creating certificate signer: %s", err)
	}
	mismatchedCert := &ssh.Certificate{Key: certKey, CertType: ssh.UserCert}
	if err := mismatchedCert.SignCert(rand.Reader, signer); err != nil {
		t.Fatalf("signing mismatched certificate: %s", err)
	}

	testCases := map[string]struct {
		privateKey    string
		certificate   string
		wantSubstring string
	}{
		"invalid private key": {
			privateKey:    "-----BEGIN SECRET KEY MATERIAL-----\nnot-a-real-private-key\n-----END SECRET KEY MATERIAL-----",
			certificate:   "unreachable-certificate",
			wantSubstring: "failed to parse private key:",
		},
		"invalid certificate": {
			privateKey:    validPrivateKey,
			certificate:   "-----BEGIN CERTIFICATE MATERIAL-----\nnot-a-real-certificate\n-----END CERTIFICATE MATERIAL-----",
			wantSubstring: "failed to parse certificate:",
		},
		"plain public key as certificate": {
			privateKey:    validPrivateKey,
			certificate:   "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKrMq5j3tsD0M+ceNKXVCRIF7De7iz0CKQqxoiyZFEeR redaction-test-marker",
			wantSubstring: `invalid certificate format "ssh-ed25519":`,
		},
		"certificate with a different public key": {
			privateKey:    validPrivateKey,
			certificate:   string(ssh.MarshalAuthorizedKey(mismatchedCert)),
			wantSubstring: "failed to create cert signer:",
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			_, err := signCertWithPrivateKey(tc.privateKey, tc.certificate)
			if err == nil {
				t.Fatal("should have had an error")
			}
			got := err.Error()
			if !strings.Contains(got, tc.wantSubstring) {
				t.Errorf("error is missing expected text\ngot:  %s\nwant substring: %s", got, tc.wantSubstring)
			}
			if strings.Contains(got, tc.privateKey) {
				t.Errorf("error leaks private key material: %s", got)
			}
			if strings.Contains(got, tc.certificate) {
				t.Errorf("error leaks certificate material: %s", got)
			}
		})
	}
}
