package gate

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// keyPath is where the gate keeps the key it signs its reports with: the
// person's config folder, which no child sees (the allowlist never binds the
// home).
func keyPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hive", "gate.key"), nil
}

// gateKey reads the key, making it first if create is set.
func gateKey(create bool) ([]byte, error) {
	p, err := keyPath()
	if err != nil {
		return nil, err
	}
	if k, err := os.ReadFile(p); err == nil && len(k) >= 32 {
		return k, nil
	} else if !create {
		return nil, fmt.Errorf("no gate key at %s: this machine's gate made no report", p)
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, k, 0o600); err != nil {
		return nil, err
	}
	return k, nil
}

// mac is the report's HMAC under key, over everything but the signature.
func (r Report) mac(key []byte) string {
	r.Sig = ""
	raw, _ := json.Marshal(r)
	h := hmac.New(sha256.New, key)
	h.Write(raw)
	return hex.EncodeToString(h.Sum(nil))
}

// sign signs the report with this machine's gate key; a report it cannot
// sign can never be installed.
func (r *Report) sign() {
	if key, err := gateKey(true); err == nil {
		r.Sig = r.mac(key)
	}
}

// Verify says the report is one this machine's gate wrote, unchanged.
func (r Report) Verify() error {
	key, err := gateKey(false)
	if err != nil {
		return err
	}
	if r.Sig == "" || !hmac.Equal([]byte(r.Sig), []byte(r.mac(key))) {
		return fmt.Errorf("the report is not one this machine's gate signed, or it was changed")
	}
	return nil
}
