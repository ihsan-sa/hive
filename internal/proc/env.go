package proc

import "bytes"

// EnvValue is key's value in env, as /proc/<pid>/environ splits it.
func EnvValue(env [][]byte, key string) string {
	prefix := []byte(key + "=")
	for _, kv := range env {
		if bytes.HasPrefix(kv, prefix) {
			return string(kv[len(prefix):])
		}
	}
	return ""
}
