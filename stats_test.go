package main

import (
	"crypto/sha256"
	"testing"
	"time"
)

func TestParseStampMixedOffsets(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"2026-11-12T09:47:29+08:00", "2026-11-12"},
		{"2026-11-13T07:16:59Z", "2026-11-13"}, // 15:16 +08:00, same local day
		{"2026-11-12T16:30:00Z", "2026-11-13"}, // 00:30 next day in +08:00
		{"", ""},
		{"garbage", ""},
	}
	for _, c := range cases {
		got, ok := parseStamp(c.in)
		if c.want == "" {
			if ok {
				t.Errorf("%q: expected failure, got %q", c.in, got)
			}
			continue
		}
		if !ok || got != c.want {
			t.Errorf("%q: got %q (ok=%v), want %q", c.in, got, ok, c.want)
		}
	}
}

func TestDayRange(t *testing.T) {
	got := dayRange("2026-11-18", 7)
	want := []string{"2026-11-12", "2026-11-13", "2026-11-14", "2026-11-15", "2026-11-16", "2026-11-17", "2026-11-18"}
	if len(got) != len(want) {
		t.Fatalf("length %d != %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %s want %s", i, got[i], want[i])
		}
	}
}

func TestStreaks(t *testing.T) {
	days := dayRange("2026-11-18", 6)
	daily := map[string]statsDay{}
	// 11-13, 11-14, 11-15 active; 11-16, 11-17 empty; 11-18 active again
	for _, d := range []string{"2026-11-13", "2026-11-14", "2026-11-15", "2026-11-18"} {
		daily[d] = statsDay{Seconds: 60}
	}
	active, current, longest := streaks(days, daily)
	if active != 4 || current != 1 || longest != 3 {
		t.Errorf("got active=%d current=%d longest=%d, want 4/1/3", active, current, longest)
	}
	// Current streak may end yesterday while today is still empty.
	delete(daily, "2026-11-18")
	daily["2026-11-17"] = statsDay{Notes: 1}
	_, current, _ = streaks(days, daily)
	if current != 1 {
		t.Errorf("current streak ending yesterday: got %d, want 1", current)
	}
}

func TestAuthToken(t *testing.T) {
	g := &authGate{enabled: true, passcode: "french2026"}
	sum := sha256Sum("francais-passcode-v1|french2026")
	g.secret = sum[:]

	token := g.issue()
	if !g.valid(token) {
		t.Fatal("fresh token should be valid")
	}
	if g.valid(token + "x") {
		t.Fatal("tampered token should be rejected")
	}
	if g.valid("123.bad") {
		t.Fatal("bad signature should be rejected")
	}
	if !g.checkPasscode("french2026") || g.checkPasscode("wrong") {
		t.Fatal("passcode comparison failed")
	}
}

// Small wrapper to keep the test readable without importing crypto/sha256 at
// the top of the production file for test-only purposes.
func sha256Sum(s string) [32]byte {
	h := sha256.New()
	h.Write([]byte(s))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func TestTodayLocalUsesFixedOffset(t *testing.T) {
	// The value must always look like a date, regardless of the machine TZ.
	got := todayLocal()
	if _, err := time.ParseInLocation("2006-01-02", got, shanghai); err != nil {
		t.Errorf("todayLocal() = %q, want YYYY-MM-DD: %v", got, err)
	}
}
