package kuma

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/icortesb/lazykuma/internal/kumatest"
)

// The fake Kuma server lives in its own package so the core and ui tests can
// use it too; these keep the names this package's tests were written with.

type fakeKuma = kumatest.Server

func newFakeKuma(t *testing.T, handle func(event string, args []json.RawMessage) any) *fakeKuma {
	t.Helper()
	return kumatest.New(t, handle)
}

func newSlowFakeKuma(t *testing.T, slowStart time.Duration, handle func(event string, args []json.RawMessage) any) *fakeKuma {
	t.Helper()
	return kumatest.NewSlow(t, slowStart, handle)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	kumatest.Eventually(t, what, cond)
}

func kumaLogin(twoFA bool) func(string, []json.RawMessage) any { return kumatest.Login(twoFA) }
