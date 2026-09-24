//go:build !linux

package inputbridge

import "errors"

// The bridge exists for Linux containers only.
type Bridge struct{}

func New() (*Bridge, error) { return nil, errors.New("input-bridge runs inside Linux containers only") }

func (b *Bridge) Run() error { return nil }
