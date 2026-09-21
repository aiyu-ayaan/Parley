package backend

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func vis(t *testing.T, a *App, id string) string {
	a.procMu.Lock()
	p := a.sessions[id].page
	a.procMu.Unlock()
	var at struct {
		SessionID string `json:"sessionId"`
	}
	if err := p.c.call("", "Target.attachToTarget", map[string]any{"targetId": p.target, "flatten": true}, &at); err != nil {
		t.Fatal(err)
	}
	var r struct{ Result struct{ Value string } }
	if err := p.c.call(at.SessionID, "Runtime.evaluate", map[string]any{"expression": "document.visibilityState + ' ' + typeof window.__wwNotify + ' ' + Notification.permission"}, &r); err != nil {
		t.Fatal(err)
	}
	return r.Result.Value
}

func roles(id string) string {
	out, _ := exec.Command("xdotool", "search", "--class", windowClass(id)).Output()
	var r []string
	for _, w := range strings.Fields(string(out)) {
		if b, err := exec.Command("xprop", "-id", w, "WM_WINDOW_ROLE").Output(); err == nil && strings.Contains(string(b), "\"") {
			r = append(r, strings.Split(string(b), "\"")[1])
		}
	}
	return strings.Join(r, ",")
}

func mapped(id string) int {
	out, _ := exec.Command("xdotool", "search", "--onlyvisible", "--class", windowClass(id)).Output()
	return len(strings.Fields(string(out)))
}

func waitReady(t *testing.T, a *App, id string) time.Duration {
	t0 := time.Now()
	for time.Since(t0) < 20*time.Second {
		a.procMu.Lock()
		s := a.sessions[id]
		ok := s != nil && s.page != nil
		a.procMu.Unlock()
		if ok {
			return time.Since(t0)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("page never ready")
	return 0
}

// TestLiveWindow drives a real Chrome through background, open, hide, reload and a user
// close. Opens a window on screen, so it only runs with PARLEY_LIVE=1.
func TestLiveWindow(t *testing.T) {
	if os.Getenv("PARLEY_LIVE") == "" {
		t.Skip()
	}
	a, cleanup := createTestApp(t)
	defer cleanup()
	p, _ := a.CreateProfile("live")
	// A real http page: Chrome won't let a page reload itself to a data: URL.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<title>live</title>hello"))
	}))
	defer srv.Close()
	p.URL = srv.URL
	if u := os.Getenv("PARLEY_URL"); u != "" {
		p.URL = u
	}
	a.UpdateProfile(p)

	a.startSession(p.ID, false)
	// A background start must never put a window on screen, not even for a frame.
	seen := 0
	for i := 0; i < 60; i++ {
		seen = max(seen, mapped(p.ID))
		time.Sleep(50 * time.Millisecond)
	}
	waitReady(t, a, p.ID)
	t.Logf("background: %s, windows ever mapped=%d, status=%+v", vis(t, a, p.ID), seen, a.GetProfileStatus(p.ID))
	if seen != 0 {
		t.Error("background start showed a window")
	}

	// First open creates the app window (one page load).
	t0 := time.Now()
	if err := a.OpenProfile(p.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200 && !a.GetProfileStatus(p.ID).HasWindow; i++ {
		time.Sleep(25 * time.Millisecond)
	}
	t.Logf("first open took %s", time.Since(t0))
	time.Sleep(500 * time.Millisecond)
	if f := os.Getenv("PARLEY_OPEN_SHOT"); f != "" {
		time.Sleep(4 * time.Second)
		out, _ := exec.Command("xdotool", "search", "--onlyvisible", "--class", windowClass(p.ID)).Output()
		if w := strings.Fields(string(out)); len(w) > 0 {
			exec.Command("xdotool", "windowactivate", "--sync", w[0]).Run()
		}
		time.Sleep(300 * time.Millisecond)
		exec.Command("gnome-screenshot", "-w", "-f", f).Run()
	}
	t.Logf("shown: %s, mapped=%d, role=%s, status=%+v", vis(t, a, p.ID), mapped(p.ID), roles(p.ID), a.GetProfileStatus(p.ID))
	if r := roles(p.ID); r != "pop-up" {
		t.Errorf("window role %q, want a bare app window (pop-up)", r)
	}

	a.HideProfileWindow(p.ID)
	time.Sleep(500 * time.Millisecond)
	t.Logf("hidden: %s, mapped=%d", vis(t, a, p.ID), mapped(p.ID))
	a.procMu.Lock()
	pg := a.sessions[p.ID].page
	a.procMu.Unlock()
	pg.c.call(pg.session, "Page.reload", nil, nil)
	time.Sleep(time.Second)
	t.Logf("hidden after reload: %s", vis(t, a, p.ID))

	a.OpenProfile(p.ID)
	time.Sleep(500 * time.Millisecond)
	t.Logf("reshown: %s, mapped=%d", vis(t, a, p.ID), mapped(p.ID))

	// User clicks X: the close is cancelled and the window just hides, same Chrome.
	a.procMu.Lock()
	old := a.sessions[p.ID].cmd
	a.procMu.Unlock()
	pg.c.call(pg.session, "Runtime.evaluate", map[string]any{"expression": "0", "userGesture": true}, nil)
	closeWindow := func() {
		out, _ := exec.Command("xdotool", "search", "--onlyvisible", "--class", windowClass(p.ID)).Output()
		for _, w := range strings.Fields(string(out)) {
			exec.Command("wmctrl", "-i", "-c", w).Run()
		}
	}
	closeWindow()
	time.Sleep(150 * time.Millisecond)
	exec.Command("gnome-screenshot", "-f", os.Getenv("PARLEY_SHOT")).Run()
	time.Sleep(time.Second)
	a.procMu.Lock()
	same := a.sessions[p.ID].cmd == old
	a.procMu.Unlock()
	t.Logf("after X: same chrome=%v, %s, mapped=%d, status=%+v", same, vis(t, a, p.ID), mapped(p.ID), a.GetProfileStatus(p.ID))
	if !same || mapped(p.ID) != 0 {
		t.Error("closing the window should hide it, not restart Chrome")
	}

	// A page-initiated reload must still go through (e.g. WhatsApp logging out).
	pg.c.call(pg.session, "Runtime.evaluate", map[string]any{"expression": "window.__marker = 1; setTimeout(() => location.reload(), 10)", "userGesture": true}, nil)
	time.Sleep(1500 * time.Millisecond)
	var mk struct{ Result struct{ Type string } }
	pg.c.call(pg.session, "Runtime.evaluate", map[string]any{"expression": "window.__marker"}, &mk)
	t.Logf("after page reload: marker=%s (undefined = reloaded)", mk.Result.Type)
	if mk.Result.Type != "undefined" {
		t.Error("page-initiated reload was blocked")
	}

	// Open again after the X-close: still instant.
	t0 = time.Now()
	a.OpenProfile(p.ID)
	t.Logf("reopen after X took %s, mapped=%d", time.Since(t0), mapped(p.ID))

	a.CloseProfile(p.ID)
	time.Sleep(2 * time.Second)
	t.Logf("stopped: %+v", a.GetProfileStatus(p.ID))

	// Second launch on the now-used profile, opened while the hidden tab is being set up.
	a.startSession(p.ID, false)
	time.Sleep(300 * time.Millisecond)
	a.OpenProfile(p.ID)
	for i := 0; i < 400 && !a.GetProfileStatus(p.ID).HasWindow; i++ {
		time.Sleep(25 * time.Millisecond)
	}
	time.Sleep(time.Second)
	a.procMu.Lock()
	cur := a.sessions[p.ID].page
	a.procMu.Unlock()
	var href struct{ Result struct{ Value string } }
	if cur != nil {
		cur.c.call(cur.session, "Runtime.evaluate", map[string]any{"expression": "location.href"}, &href)
	}
	t.Logf("open during startup: status=%+v, page=%q, windows=%d", a.GetProfileStatus(p.ID), href.Result.Value, mapped(p.ID))
	if !a.GetProfileStatus(p.ID).HasWindow || strings.HasPrefix(href.Result.Value, "data:") {
		t.Error("window stuck or missing when opened during startup")
	}
	a.CloseProfile(p.ID)
	time.Sleep(2 * time.Second)
}
