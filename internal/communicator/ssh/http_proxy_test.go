// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package ssh

import (
	"bufio"
	"net"
	"strings"
	"testing"
)

type stubForwardDialer struct {
	conn net.Conn
	err  error
}

func (d stubForwardDialer) Dial(network, addr string) (net.Conn, error) {
	return d.conn, d.err
}

func TestProxyDialer_Dial(t *testing.T) {
	cases := []struct {
		name     string
		response string
		wantErr  string
	}{
		{
			name:     "closed without response",
			response: "",
			wantErr:  "EOF",
		},
		{
			name:     "malformed status line",
			response: "garbage\r\n\r\n",
			wantErr:  "malformed HTTP response",
		},
		{
			name:     "proxied success 200",
			response: "HTTP/1.1 200 Connection Established\r\n\r\n",
			wantErr:  "",
		},
		{
			name:     "proxy auth required 407",
			response: "HTTP/1.1 407 Proxy Authentication Required\r\n\r\n",
			wantErr:  "StatusCode: 407",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			t.Cleanup(func() {
				client.Close()
				server.Close()
			})

			go func() {
				defer server.Close()
				// Read the CONNECT request before replying so the
				// synchronous pipe write doesn't deadlock.
				br := bufio.NewReader(server)
				for {
					line, err := br.ReadString('\n')
					if err != nil {
						return
					}
					if strings.TrimSpace(line) == "" {
						break
					}
				}
				if tc.response != "" {
					if _, err := server.Write([]byte(tc.response)); err != nil {
						t.Errorf("write proxy response: %v", err)
					}
				}
			}()

			dialer := &proxyDialer{
				proxy:   *newProxyInfo("proxy.example.com:3128", "http", "", ""),
				forward: stubForwardDialer{conn: client},
			}

			conn, err := dialer.Dial("tcp", "host.example.com:22")

			if tc.wantErr == "" {
				if err != nil || conn == nil {
					t.Fatalf("expected connection, got err=%v, conn=%v", err, conn)
				}
				return
			}

			if err == nil {
				t.Fatalf("expected error containing %q, got nil with conn %v", tc.wantErr, conn)
			}
			if conn != nil {
				t.Error("expected nil conn together with error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}
