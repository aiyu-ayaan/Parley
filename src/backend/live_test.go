package backend

import (
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
	p.URL = "data:text/html,<title>live</title>hello"
	a.UpdateProfile(p)

	a.startSession(p.ID, false)
	t.Logf("background ready in %s", waitReady(t, a, p.ID))
	time.Sleep(500 * time.Millisecond)
	t.Logf("background: %s, mapped=%d, status=%+v", vis(t, a, p.ID), mapped(p.ID), a.GetProfileStatus(p.ID))

	t0 := time.Now()
	if err := a.OpenProfile(p.ID); err != nil {
		t.Fatal(err)
	}
	t.Logf("open took %s", time.Since(t0))
	time.Sleep(500 * time.Millisecond)
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

	// User closes the window: Chrome quits and comes back hidden.
	a.procMu.Lock()
	old := a.sessions[p.ID].cmd
	a.procMu.Unlock()
	out, _ := exec.Command("xdotool", "search", "--onlyvisible", "--class", windowClass(p.ID)).Output()
	for _, w := range strings.Fields(string(out)) {
		exec.Command("wmctrl", "-i", "-c", w).Run()
	}
	for i := 0; i < 100; i++ {
		a.procMu.Lock()
		s := a.sessions[p.ID]
		back := s.cmd != nil && s.cmd != old && s.page != nil
		a.procMu.Unlock()
		if back {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	t.Logf("after close: %s, mapped=%d, status=%+v", vis(t, a, p.ID), mapped(p.ID), a.GetProfileStatus(p.ID))

	a.CloseProfile(p.ID)
	time.Sleep(2 * time.Second)
	t.Logf("stopped: %+v", a.GetProfileStatus(p.ID))
}
