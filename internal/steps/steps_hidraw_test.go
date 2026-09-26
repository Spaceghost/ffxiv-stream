package steps

import (
	"testing"

	"github.com/Spaceghost/ffxiv-stream/internal/config"
)

func TestHidrawHostDir(t *testing.T) {
	c := config.Default()
	c.Incus.Container = "ffxiv"
	for _, tc := range []struct {
		backend, pad, want string
	}{
		{config.BackendSunshine, "x360", ""},
		{config.BackendSunshine, "", ""},
		{config.BackendSunshine, "ds5", "/dev/ffxiv-stream/ffxiv"},
		{config.BackendSunshine, "auto", "/dev/ffxiv-stream/ffxiv"},
		{config.BackendWolf, "ds5", ""},
	} {
		c.Backend, c.Stream.Gamepad = tc.backend, tc.pad
		if got := HidrawHostDir(c); got != tc.want {
			t.Errorf("%s/%s: %q, want %q", tc.backend, tc.pad, got, tc.want)
		}
	}
}
