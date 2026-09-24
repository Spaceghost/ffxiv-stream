package steps

import "github.com/Spaceghost/ffxiv-stream/internal/assets"

func mustRender(name string, v any) []byte { return assets.MustRender(name, v) }
