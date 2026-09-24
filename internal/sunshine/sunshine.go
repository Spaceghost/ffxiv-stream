// Package sunshine talks to Sunshine's web API: pairing a Moonlight client by
// its PIN without opening the web UI.
package sunshine

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Spaceghost/ffxiv-stream/internal/plan"
)

// Pair submits a Moonlight PIN. Sunshine's web UI listens on base+1 on the
// machine it runs on; t is that machine (a container, or this one).
func Pair(t plan.Target, port int, user, password, pin, name string) error {
	if len(pin) != 4 || strings.Trim(pin, "0123456789") != "" {
		return fmt.Errorf("a Moonlight PIN is four digits")
	}
	body, _ := json.Marshal(map[string]string{"pin": pin, "name": name})
	url := fmt.Sprintf("https://127.0.0.1:%d/api/pin", port)
	// The client must have asked to pair first; Sunshine rejects a PIN it is
	// not waiting for. A few tries cover a PIN typed just before Moonlight's
	// request reached the server.
	var last string
	for i := 0; i < 5; i++ {
		out, err := t.RunInput([]byte(password), "sh", "-c",
			`curl -sk -m 10 -u "$1:$(cat)" -H content-type:application/json -d "$2" "$3"`, "-", user, string(body), url)
		if err != nil {
			return err
		}
		last = out
		var reply struct {
			Status any `json:"status"`
		}
		if json.Unmarshal([]byte(out), &reply) == nil && fmt.Sprint(reply.Status) == "true" {
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("sunshine did not accept the PIN (is Moonlight showing it right now?): %s", last)
}
