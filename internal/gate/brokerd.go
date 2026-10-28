package gate

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// ServeBroker answers CONNECTs on ln with b: GitHub's hosts are the broker's,
// anything else is refused (the caller's own proxy carries the rest). It
// returns when ln is closed.
func ServeBroker(ln net.Listener, b *Broker) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			c.SetReadDeadline(time.Now().Add(30 * time.Second))
			br := bufio.NewReader(c)
			req, err := http.ReadRequest(br)
			if err != nil {
				return
			}
			if req.Method != http.MethodConnect || !b.Intercepts(req.Host) {
				io.WriteString(c, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
				return
			}
			c.SetReadDeadline(time.Time{})
			io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n")
			b.Serve(&countedConn{Conn: c, r: br, n: new(atomic.Int64)})
		}()
	}
}

// BrokerMain is `hive gate-broker`: a broker for a harness that runs
// sub-Hives in a box of its own (an eval's arms), outside that box. It holds
// the org's token, makes the run's fresh private repositories when asked
// (--create) and deletes them when it ends (SIGINT or SIGTERM), and serves
// CONNECTs for GitHub's hosts on a unix socket, which the box's own proxy
// relays GitHub's CONNECTs to. It writes the trust file the box points
// SSL_CERT_FILE and GIT_SSL_CAINFO at, then one JSON line on stdout once it
// is serving: {"sock", "trust", "repos", "env"}.
func BrokerMain(args []string) int {
	fs := flag.NewFlagSet("gate-broker", flag.ContinueOnError)
	owner := fs.String("owner", "Hive-RSI-Lab", "the test org")
	tokenFile := fs.String("token-file", "", "the org's token (else HIVE_GATE_ORG_TOKEN)")
	sock := fs.String("sock", "", "the unix socket to serve on")
	trust := fs.String("trust", "", "where to write the box's trust file")
	create := fs.Bool("create", false, "make the repositories now, private, and delete them on exit")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: hive gate-broker --sock <path> --trust <path> [--owner Hive-RSI-Lab] [--token-file f] [--create] <repo>...")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repos := fs.Args()
	if *sock == "" || *trust == "" || len(repos) == 0 {
		fs.Usage()
		return 2
	}
	tok := os.Getenv("HIVE_GATE_ORG_TOKEN")
	if *tokenFile != "" {
		raw, err := os.ReadFile(*tokenFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gate-broker:", err)
			return 2
		}
		tok = strings.TrimSpace(string(raw))
	}
	b, err := NewBroker(*owner, tok)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gate-broker:", err)
		return 2
	}
	b.Log = func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) }
	org := GitHub{Owner: *owner, Token: tok}
	var made []string
	defer func() {
		for _, r := range made {
			if err := org.Delete(r); err != nil {
				fmt.Fprintln(os.Stderr, "gate-broker: left", *owner+"/"+r+":", err)
			}
		}
	}()
	for _, r := range repos {
		if strings.Contains(r, "/") {
			fmt.Fprintln(os.Stderr, "gate-broker: a repository is named without its owner:", r)
			return 2
		}
		if *create {
			if _, err := org.Create(r); err != nil {
				fmt.Fprintln(os.Stderr, "gate-broker:", err)
				return 1
			}
			made = append(made, r)
		}
		b.Allow(r)
	}
	if err := b.WriteTrust(*trust); err != nil {
		fmt.Fprintln(os.Stderr, "gate-broker:", err)
		return 1
	}
	os.Remove(*sock)
	ln, err := net.Listen("unix", *sock)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gate-broker:", err)
		return 1
	}
	defer os.Remove(*sock)
	go ServeBroker(ln, b)
	json.NewEncoder(os.Stdout).Encode(map[string]any{"sock": *sock, "trust": *trust, "repos": repos, "env": TrustEnv(*trust)})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	ln.Close()
	return 0
}
