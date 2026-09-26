package steps

import "github.com/Spaceghost/xivstream-dalamud/internal/assets"

func mustRender(name string, v any) []byte { return assets.MustRender(name, v) }
