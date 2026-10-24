package gate

import (
	"encoding/base64"
)

// basic is the Authorization value git over HTTPS takes for a GitHub token.
func basic(token string) string {
	return base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
}

func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}
