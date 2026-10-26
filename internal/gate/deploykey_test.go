package gate

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A child with a deploy key reaches GitHub over ssh with that key alone,
// trusting GitHub's pinned host key, through gate-connect and so the meter.
func TestDeployKeySSHConfig(t *testing.T) {
	dir := t.TempDir()
	c := &Child{Name: "candidate", Root: dir, Key: filepath.Join(dir, "deploy-key"), Fwd: "/opt/hive bin/hive"}
	if err := c.writeSSHConfig(); err != nil {
		t.Fatal(err)
	}
	conf, _ := os.ReadFile(c.Key + ".ssh_config")
	for _, want := range []string{`IdentityFile "` + c.Key + `"`, "IdentitiesOnly yes", "StrictHostKeyChecking yes", "GlobalKnownHostsFile /dev/null", `ProxyCommand "/opt/hive bin/hive" gate-connect %h %p`} {
		if !strings.Contains(string(conf), want) {
			t.Errorf("ssh config lacks %q:\n%s", want, conf)
		}
	}
	kh, _ := os.ReadFile(c.Key + ".known_hosts")
	if string(kh) != "[ssh.github.com]:443 "+githubHostKey+"\n" {
		t.Errorf("known_hosts %q", kh)
	}
	var cmd string
	for _, e := range c.Env() {
		if v, ok := strings.CutPrefix(e, "GIT_SSH_COMMAND="); ok {
			cmd = v
		}
		if strings.HasPrefix(e, "GH_TOKEN=") {
			t.Errorf("a child with a deploy key holds a token: %s", e)
		}
	}
	if cmd != "ssh -F '"+c.Key+".ssh_config'" {
		t.Errorf("GIT_SSH_COMMAND %q", cmd)
	}
	if _, err := exec.LookPath("ssh"); err == nil {
		// ssh itself accepts the config.
		if out, err := exec.Command("ssh", "-G", "-F", c.Key+".ssh_config", "-p", "443", "git@ssh.github.com").CombinedOutput(); err != nil {
			t.Errorf("ssh -G: %v %s", err, out)
		}
	}
}

// gate-connect tunnels through the proxy in HTTPS_PROXY and relays both ways.
func TestConnectTunnels(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 2)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		got <- req.Method + " " + req.Host
		io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\nSSH-2.0-test\r\n")
		line, _ := br.ReadString('\n')
		got <- line
	}()
	t.Setenv("HTTPS_PROXY", "http://"+ln.Addr().String())
	stdin, w, _ := os.Pipe()
	r, stdout, _ := os.Pipe()
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = stdin, stdout
	defer func() { os.Stdin, os.Stdout = oldIn, oldOut }()
	io.WriteString(w, "SSH-2.0-client\n")
	w.Close()
	code := Connect("ssh.github.com", "443")
	stdout.Close()
	out, _ := io.ReadAll(r)
	if code != 0 || string(out) != "SSH-2.0-test\r\n" {
		t.Errorf("code %d, out %q", code, out)
	}
	if m := <-got; m != "CONNECT ssh.github.com:443" {
		t.Errorf("request %q", m)
	}
	if l := <-got; l != "SSH-2.0-client\n" {
		t.Errorf("relayed %q", l)
	}
}
