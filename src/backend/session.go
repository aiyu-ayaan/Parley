package backend

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// session is one profile's supervised Chrome. Chrome starts with no window and WhatsApp
// in a hidden (windowless) tab, so a background start never flashes anything. The first
// Open moves WhatsApp into an app window; from then on closing or hiding that window
// only unmaps it, so opening again is instant.
type session struct {
	cmd        *exec.Cmd
	c          *cdp   // DevTools connection of the running Chrome
	page       *page  // current WhatsApp tab, nil until it is set up
	expect     string // "hidden" or "window": the kind of tab being created
	window     bool   // window is shown
	wantWindow bool   // show the window once the page is ready
	stopped    bool   // user stopped it; supervisor exits
	wake       chan struct{}
}

// page is the live WhatsApp tab of a session.
type page struct {
	mu          sync.Mutex // serialises show/hide
	c           *cdp
	target      string
	session     string // DevTools session attached to the tab
	windowID    int    // 0 for the windowless background tab
	stateScript string // identifier of the injected __parleyHidden script
}

// poke cuts short a supervisor's backoff so a start, open or stop acts immediately.
func (s *session) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *session) sleep(d time.Duration) {
	select {
	case <-s.wake:
	case <-time.After(d):
	}
}

const (
	minBackoff = 5 * time.Second
	maxBackoff = 5 * time.Minute
)

// notifyHook replaces the page's Notification API so every notification WhatsApp raises
// is handed to Go, which shows it with notify-send and opens the account on click. It
// also makes the page report itself hidden unless Parley has shown the window
// (window.__parleyHidden === false), so WhatsApp notifies and never marks chats read
// in the background. Minimising alone doesn't reliably do that on every WM.
const notifyHook = `(() => {
  const send = (t, o) => { try { window.__wwNotify(JSON.stringify({title: String(t || ''), body: String((o && o.body) || '')})); } catch (e) {} };
  class N extends EventTarget {
    constructor(t, o) { super(); this.title = t; Object.assign(this, o || {}); send(t, o); }
    close() {}
    static get permission() { return 'granted'; }
    static requestPermission(cb) { if (cb) cb('granted'); return Promise.resolve('granted'); }
  }
  window.Notification = N;
  if (window.ServiceWorkerRegistration) ServiceWorkerRegistration.prototype.showNotification = function (t, o) { send(t, o); return Promise.resolve(); };
  const P = Document.prototype, hid = Object.getOwnPropertyDescriptor(P, 'hidden').get, focus = P.hasFocus;
  const off = (d) => window.__parleyHidden !== false || hid.call(d);
  Object.defineProperty(P, 'hidden', {configurable: true, get() { return off(this); }});
  Object.defineProperty(P, 'visibilityState', {configurable: true, get() { return off(this) ? 'hidden' : 'visible'; }});
  P.hasFocus = function () { return !off(this) && focus.call(this); };
  // Mute WhatsApp's own new-message tone; notify-send plays the system sound instead.
  // Its module loader is assigned as window.__d, so wrap it and stub playNotification
  // on the WAWebNotificationTone module's exports. Ringtones and voice notes are untouched.
  if (window === top) {
    const quiet = (f) => function () { const r = f.apply(this, arguments); for (const x of arguments) if (x && typeof x.playNotification === 'function') x.playNotification = () => {}; return r; };
    let d;
    Object.defineProperty(window, '__d', {configurable: true, get() { return d; }, set(fn) {
      d = typeof fn !== 'function' ? fn : function (n, deps, f, fl) { return fn.call(this, n, deps, n === 'WAWebNotificationTone' && typeof f === 'function' ? quiet(f) : f, fl); };
    }});
  }
  // Closing the window runs beforeunload; Parley answers the dialog, cancelling a
  // close (and hiding the window instead) but letting real navigations through.
  if (window === top) addEventListener('beforeunload', (e) => { if (!window.__parleyLeave) { e.preventDefault(); e.returnValue = ''; } });
})();`

func (a *App) sessionDir(id string) string {
	return filepath.Join(a.encryptionService.DataDir(), "sessions", id)
}

// startSession starts the supervisor for a profile, or wakes the existing one.
func (a *App) startSession(id string, window bool) {
	a.procMu.Lock()
	defer a.procMu.Unlock()
	if a.shuttingDown {
		return
	}
	if s, ok := a.sessions[id]; ok {
		s.stopped = false
		s.poke()
		if window && s.page != nil {
			go a.setVisible(id, true)
		} else if window {
			s.wantWindow = true
		}
		return
	}
	a.sessions[id] = &session{wantWindow: window, wake: make(chan struct{}, 1)}
	go a.supervise(id)
}

// supervise keeps a profile's Chrome alive until it is stopped. Closing the window
// quits Chrome, and the loop brings it straight back up hidden.
func (a *App) supervise(id string) {
	backoff := minBackoff
	for {
		a.procMu.Lock()
		s := a.sessions[id]
		if s == nil || s.stopped || a.shuttingDown {
			delete(a.sessions, id)
			a.procMu.Unlock()
			a.emitStatus(id)
			return
		}
		a.procMu.Unlock()

		cmd, err := a.launch(id, s)
		if err != nil {
			log.Printf("session %s: launch failed: %v (retry in %s)", id, err, backoff)
			s.sleep(backoff)
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		a.procMu.Lock()
		s.cmd = cmd
		a.procMu.Unlock()
		a.emitStatus(id)

		started := time.Now()
		_ = cmd.Wait()

		a.procMu.Lock()
		s.cmd, s.page, s.window = nil, nil, false
		requested := s.stopped || a.shuttingDown
		a.procMu.Unlock()
		a.emitStatus(id)

		// Crash-loop guard: an unrequested exit this fast means Chrome can't start.
		// Back off exponentially so a broken setup doesn't burn CPU all day.
		if !requested && time.Since(started) < 3*time.Second {
			log.Printf("session %s: chrome exited after %s, retry in %s", id, time.Since(started).Round(time.Millisecond), backoff)
			s.sleep(backoff)
			backoff = min(backoff*2, maxBackoff)
		} else {
			backoff = minBackoff
		}
	}
}

// launch starts Chrome for the profile, windowless, driven over a DevTools pipe.
func (a *App) launch(id string, s *session) (*exec.Cmd, error) {
	a.mu.RLock()
	_, ok := a.profiles[id]
	a.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("profile %s not found", id)
	}
	dir := a.sessionDir(id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := ensureSessionPreferences(dir); err != nil {
		log.Printf("session %s: preferences: %v", id, err)
	}

	evictStale(dir)

	chrome, err := findChrome()
	if err != nil {
		return nil, err
	}
	args := []string{
		"--user-data-dir=" + dir,
		"--no-first-run",
		"--no-default-browser-check",
		"--password-store=basic",
		"--disable-sync",
		"--disable-default-apps",
		"--disable-component-update",
		"--disable-extensions",
		"--disable-background-networking",
		// Commands on fd 3, replies on fd 4. No TCP port, so no other local process
		// can drive the session.
		"--remote-debugging-pipe",
		"--no-startup-window",
		"--class=" + windowClass(id),
		"--window-size=1100,800",
	}

	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, err
	}
	cmd := exec.Command(chrome, args...)
	cmd.ExtraFiles = []*os.File{inR, outW}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err = cmd.Start()
	inR.Close()
	outW.Close()
	if err != nil {
		inW.Close()
		outR.Close()
		return nil, err
	}

	c := newCDP(inW, nil)
	c.onEvent = a.onEvent(id, c)
	go func() {
		defer inW.Close()
		if err := c.readLoop(outR); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
			log.Printf("session %s: devtools: %v", id, err)
		}
		outR.Close()
	}()
	a.procMu.Lock()
	s.c = c
	show := s.wantWindow
	a.procMu.Unlock()
	go func() {
		// Target events (targetDestroyed) tell pageGone when the WhatsApp tab vanishes.
		err := c.call("", "Target.setDiscoverTargets", map[string]any{"discover": true}, nil)
		if err == nil && show {
			err = a.openWindow(id)
		} else if err == nil {
			err = a.openHidden(id)
		}
		if err != nil {
			log.Printf("session %s: setup: %v", id, err)
			killGroup(cmd)
		}
	}()
	return cmd, nil
}

func profileURL(p *Profile) string {
	if p.URL == "" {
		return "https://web.whatsapp.com"
	}
	return p.URL
}

// openHidden loads WhatsApp in a windowless tab: running, notifying, never on screen.
func (a *App) openHidden(id string) error {
	a.procMu.Lock()
	s := a.sessions[id]
	a.mu.RLock()
	p := a.profiles[id]
	a.mu.RUnlock()
	if s == nil || s.c == nil || p == nil {
		a.procMu.Unlock()
		return errors.New("session not running")
	}
	s.expect = "hidden"
	c := s.c
	a.procMu.Unlock()
	var t struct {
		TargetID string `json:"targetId"`
	}
	if err := c.call("", "Target.createTarget", map[string]any{"url": "about:blank", "hidden": true, "background": true}, &t); err != nil {
		return err
	}
	var at struct {
		SessionID string `json:"sessionId"`
	}
	if err := c.call("", "Target.attachToTarget", map[string]any{"targetId": t.TargetID, "flatten": true}, &at); err != nil {
		return err
	}
	a.adoptPage(id, c, at.SessionID, t.TargetID, "hidden")
	return nil
}

// windowStub is the page an app window opens on. It has to be a real URL (Chrome
// opens --app=about:blank as a tabbed window), and it must not be WhatsApp itself,
// which would start before the hooks are in; adoptPage navigates it to WhatsApp.
const windowStub = "data:text/html,<title>WhatsApp</title><body style=background:%23111b21>"

// openWindow asks the running Chrome for a WhatsApp app window. There is no DevTools
// command for app windows, so it launches Chrome again with --app: that instance hands
// the request to the running one and exits. The new tab is then found by its stub URL.
func (a *App) openWindow(id string) error {
	a.procMu.Lock()
	s := a.sessions[id]
	a.mu.RLock()
	p := a.profiles[id]
	a.mu.RUnlock()
	if s == nil || s.cmd == nil && s.c == nil || p == nil {
		a.procMu.Unlock()
		return errors.New("session not running")
	}
	s.wantWindow = true
	if s.expect == "window" {
		a.procMu.Unlock()
		return nil // already on its way
	}
	s.expect = "window"
	a.procMu.Unlock()

	err := a.handoffWindow(id, p)
	a.procMu.Lock()
	if s.expect == "window" {
		s.expect = "" // never leave a stale claim that blocks the next Open
	}
	a.procMu.Unlock()
	return err
}

func (a *App) handoffWindow(id string, p *Profile) error {
	a.procMu.Lock()
	c := a.sessions[id].c
	a.procMu.Unlock()
	chrome, err := findChrome()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, chrome, "--user-data-dir="+a.sessionDir(id), "--class="+windowClass(id), "--app="+windowStub).Run(); err != nil {
		return err
	}
	for i := 0; i < 100; i++ {
		var r struct {
			TargetInfos []struct {
				TargetID string `json:"targetId"`
				Type     string `json:"type"`
				URL      string `json:"url"`
			} `json:"targetInfos"`
		}
		if err := c.call("", "Target.getTargets", nil, &r); err != nil {
			return err
		}
		for _, t := range r.TargetInfos {
			if t.Type == "page" && strings.HasPrefix(t.URL, "data:text/html,") && strings.Contains(t.URL, "WhatsApp") {
				var at struct {
					SessionID string `json:"sessionId"`
				}
				if err := c.call("", "Target.attachToTarget", map[string]any{"targetId": t.TargetID, "flatten": true}, &at); err != nil {
					return err
				}
				a.adoptPage(id, c, at.SessionID, t.TargetID, "window")
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("app window did not appear")
}

// adoptPage installs the hooks in a new blank tab, makes it the session's WhatsApp page
// and loads WhatsApp in it. kind must match what the session is expecting, so tabs
// Parley didn't ask for (links WhatsApp opens) are left alone.
func (a *App) adoptPage(id string, c *cdp, sid, target, kind string) {
	a.procMu.Lock()
	s := a.sessions[id]
	a.mu.RLock()
	prof := a.profiles[id]
	a.mu.RUnlock()
	if s == nil || s.c != c || s.expect != kind || prof == nil {
		a.procMu.Unlock()
		// Superseded (e.g. Open while the background tab was being set up): drop it.
		_ = c.call("", "Target.closeTarget", map[string]any{"targetId": target}, nil)
		return
	}
	s.expect = ""
	old := s.page
	a.procMu.Unlock()

	err := func() error {
		for _, st := range []struct {
			method string
			params any
		}{
			{"Runtime.addBinding", map[string]any{"name": "__wwNotify"}},
			{"Runtime.enable", nil},
			{"Page.enable", nil},
			{"Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": notifyHook}},
			{"Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": "window.__parleyHidden = true"}},
		} {
			if err := c.call(sid, st.method, st.params, nil); err != nil {
				return err
			}
		}
		return nil
	}()
	pg := &page{c: c, target: target, session: sid}
	if kind == "window" {
		var w struct {
			WindowID int `json:"windowId"`
		}
		if err == nil {
			err = c.call("", "Browser.getWindowForTarget", map[string]any{"targetId": target}, &w)
		}
		pg.windowID = w.WindowID
	}
	if err != nil {
		log.Printf("session %s: page setup: %v", id, err)
		return
	}
	// WhatsApp allows one tab per login: retire the old one before the new one starts.
	if old != nil {
		_ = c.call(old.session, "Runtime.evaluate", map[string]any{"expression": "window.__parleyLeave = true"}, nil)
		_ = c.call("", "Target.closeTarget", map[string]any{"targetId": old.target}, nil)
	}
	a.procMu.Lock()
	s.page = pg
	show := s.wantWindow && kind == "window"
	s.wantWindow = false
	a.procMu.Unlock()
	if err := c.call(sid, "Page.navigate", map[string]any{"url": profileURL(prof)}, nil); err != nil {
		log.Printf("session %s: load: %v", id, err)
	}
	if err := a.setVisible(id, show); err != nil {
		log.Printf("session %s: window: %v", id, err)
	}
}

// pageGone reloads WhatsApp in a hidden tab if its tab disappeared while Chrome lives
// on (e.g. the window closed without asking).
func (a *App) pageGone(id string, c *cdp, target string) {
	a.procMu.Lock()
	s := a.sessions[id]
	gone := s != nil && s.c == c && s.page != nil && s.page.target == target
	if gone {
		s.page, s.window = nil, false
	}
	a.procMu.Unlock()
	if !gone {
		return
	}
	a.emitStatus(id)
	if err := a.openHidden(id); err != nil {
		log.Printf("session %s: reopen: %v", id, err)
	}
}

func windowClass(id string) string { return "parley-" + id }

// onEvent handles DevTools events for one Chrome launch. It runs on the pipe reader, so
// anything that makes a DevTools call goes to its own goroutine.
func (a *App) onEvent(id string, c *cdp) func(cdpMsg) {
	// Frames with a navigation under way. Chrome reports one (frameRequestedNavigation
	// from the page, frameStartedNavigating from the browser) before running
	// beforeunload; a window close reports none. That tells the two apart.
	navigating := map[string]bool{}
	return func(m cdpMsg) {
		var p struct {
			Name, Payload, FrameID, Type, TargetID string
			Frame                                  struct{ ID string }
		}
		_ = json.Unmarshal(m.Params, &p)
		switch m.Method {
		case "Target.targetDestroyed":
			go a.pageGone(id, c, p.TargetID)
		case "Runtime.bindingCalled":
			var n struct{ Title, Body string }
			if p.Name == "__wwNotify" && json.Unmarshal([]byte(p.Payload), &n) == nil {
				go a.desktopNotify(id, n.Title, n.Body)
			}
		case "Page.frameRequestedNavigation", "Page.frameStartedNavigating":
			navigating[p.FrameID] = true
		case "Page.frameNavigated":
			delete(navigating, p.Frame.ID)
		case "Page.navigatedWithinDocument", "Page.frameStoppedLoading", "Page.frameClearedScheduledNavigation", "Page.javascriptDialogClosed":
			delete(navigating, p.FrameID)
		case "Page.loadEventFired":
			// beforeunload only asks when the page has had a user gesture.
			go c.call(m.SessionID, "Runtime.evaluate", map[string]any{"expression": "0", "userGesture": true}, nil)
		case "Page.javascriptDialogOpening":
			if p.Type != "beforeunload" {
				return
			}
			nav := navigating[p.FrameID]
			go func() {
				a.procMu.Lock()
				s := a.sessions[id]
				quitting := s == nil || s.stopped || a.shuttingDown
				a.procMu.Unlock()
				leave := nav || quitting
				if !leave {
					x11Window(id, "windowunmap") // before the dialog can paint
				}
				_ = c.call(m.SessionID, "Page.handleJavaScriptDialog", map[string]any{"accept": leave}, nil)
				if !leave {
					_ = a.setVisible(id, false)
				}
			}()
		}
	}
}

// setVisible shows or hides a running session's window. Hidden means minimised, which
// also makes the page report itself hidden so WhatsApp notifies and doesn't mark chats
// read. On X11 the window is unmapped too, so it leaves the taskbar.
func (a *App) setVisible(id string, visible bool) error {
	a.procMu.Lock()
	s := a.sessions[id]
	var p *page
	if s != nil {
		p = s.page
	}
	a.procMu.Unlock()
	if p == nil {
		return errors.New("session not ready")
	}
	if p.windowID == 0 && visible {
		// Background tab: WhatsApp moves into a fresh app window (one load, then instant).
		go func() {
			if err := a.openWindow(id); err != nil {
				log.Printf("session %s: open window: %v", id, err)
			}
		}()
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.setHidden(!visible); err != nil {
		return err
	}
	bounds := func(b map[string]any) error {
		return p.c.call("", "Browser.setWindowBounds", map[string]any{"windowId": p.windowID, "bounds": b}, nil)
	}
	switch {
	case p.windowID == 0:
		// Windowless tab: nothing on screen to hide.
	case visible:
		x11Window(id, "windowmap")
		if err := bounds(map[string]any{"windowState": "normal"}); err != nil {
			return err
		}
		_ = p.c.call("", "Target.activateTarget", map[string]any{"targetId": p.target}, nil)
		x11Window(id, "windowactivate")
	default:
		if err := bounds(map[string]any{"windowState": "minimized"}); err != nil {
			return err
		}
		x11Window(id, "windowunmap")
	}

	a.procMu.Lock()
	if s.page == p {
		s.window = visible
	}
	a.procMu.Unlock()
	a.emitStatus(id)
	return nil
}

// setHidden tells the page whether it is hidden, now and after any reload.
func (p *page) setHidden(hidden bool) error {
	js := fmt.Sprintf("window.__parleyHidden = %t", hidden)
	if p.stateScript != "" {
		_ = p.c.call(p.session, "Page.removeScriptToEvaluateOnNewDocument", map[string]any{"identifier": p.stateScript}, nil)
	}
	var added struct {
		Identifier string `json:"identifier"`
	}
	if err := p.c.call(p.session, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": js}, &added); err != nil {
		return err
	}
	p.stateScript = added.Identifier
	return p.c.call(p.session, "Runtime.evaluate", map[string]any{
		"expression": js + "; document.dispatchEvent(new Event('visibilitychange')); window.dispatchEvent(new Event('" + map[bool]string{true: "blur", false: "focus"}[hidden] + "'))",
	}, nil)
}

// cdp is a minimal DevTools client over Chrome's NUL-delimited pipe. Calls may come
// from any goroutine; readLoop routes replies to them and events to onEvent.
type cdp struct {
	mu      sync.Mutex
	w       io.Writer
	nextID  int
	pending map[int]chan cdpMsg
	onEvent func(cdpMsg)
}

type cdpMsg struct {
	ID        int             `json:"id"`
	SessionID string          `json:"sessionId"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params"`
	Result    json.RawMessage `json:"result"`
	Error     *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func newCDP(w io.Writer, onEvent func(cdpMsg)) *cdp {
	return &cdp{w: w, pending: map[int]chan cdpMsg{}, onEvent: onEvent}
}

func (c *cdp) readLoop(r io.Reader) error {
	br := bufio.NewReader(r)
	defer func() {
		c.mu.Lock()
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.pending = nil
		c.mu.Unlock()
	}()
	for {
		b, err := br.ReadBytes(0)
		if err != nil {
			return err
		}
		var m cdpMsg
		if json.Unmarshal(b[:len(b)-1], &m) != nil {
			continue
		}
		if m.ID == 0 {
			c.onEvent(m)
			continue
		}
		c.mu.Lock()
		ch := c.pending[m.ID]
		delete(c.pending, m.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
}

// call sends a command and waits for its reply.
func (c *cdp) call(session, method string, params any, out any) error {
	msg := map[string]any{"method": method}
	if params != nil {
		msg["params"] = params
	}
	if session != "" {
		msg["sessionId"] = session
	}
	ch := make(chan cdpMsg, 1)
	c.mu.Lock()
	if c.pending == nil {
		c.mu.Unlock()
		return io.EOF
	}
	c.nextID++
	msg["id"] = c.nextID
	c.pending[c.nextID] = ch
	b, err := json.Marshal(msg)
	if err == nil {
		_, err = c.w.Write(append(b, 0))
	}
	c.mu.Unlock()
	if err != nil {
		return err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return io.EOF
		}
		if m.Error != nil {
			return fmt.Errorf("%s: %s", method, m.Error.Message)
		}
		if out != nil {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-time.After(15 * time.Second):
		return fmt.Errorf("%s: timed out", method)
	}
}

// desktopNotify shows a notification; clicking it opens the profile's window.
func (a *App) desktopNotify(id, title, body string) {
	a.mu.RLock()
	name := "WhatsApp"
	if p, ok := a.profiles[id]; ok {
		name = "WhatsApp · " + p.Name
	}
	a.mu.RUnlock()

	// notify-send -A blocks until the notification is clicked or closed; cap it so a busy
	// chat can't pile up waiting processes.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "notify-send", "-a", name, "-i", "parley", "-u", "normal", "-h", "string:sound-name:message-new-instant", "-A", "default=Open", title, body).Output()
	if err != nil && len(out) == 0 {
		// Older notify-send without actions.
		_ = exec.Command("notify-send", "-a", name, title, body).Run()
		return
	}
	if strings.TrimSpace(string(out)) == "default" {
		_ = a.OpenProfile(id)
	}
}

// evictStale stops a leftover Chrome (e.g. from an older Parley run) that still owns
// the session dir. Otherwise Chrome's singleton handoff swallows our launch and opens a
// window in the old process on every retry.
func evictStale(dir string) {
	target, err := os.Readlink(filepath.Join(dir, "SingletonLock"))
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(target[strings.LastIndex(target, "-")+1:])
	if err != nil || pid <= 0 {
		return
	}
	// Only touch it if that PID really is a Chrome on this session dir (PIDs get reused).
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	// Chrome rewrites its argv space-separated, so accept either separator.
	arg := "--user-data-dir=" + dir
	if err != nil || !(strings.Contains(string(cmdline), arg+" ") || strings.Contains(string(cmdline), arg+"\x00")) {
		return
	}
	log.Printf("stopping leftover chrome %d on %s", pid, dir)
	_ = syscall.Kill(pid, syscall.SIGTERM)
	for i := 0; i < 50 && syscall.Kill(pid, 0) == nil; i++ {
		time.Sleep(100 * time.Millisecond)
	}
}

func killGroup(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
}

func findChrome() (string, error) {
	for _, b := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "brave-browser", "microsoft-edge"} {
		if p, err := exec.LookPath(b); err == nil {
			return p, nil
		}
	}
	return "", errors.New("no Chromium-based browser found (install google-chrome or chromium)")
}

// x11Window runs an xdotool window command (windowmap, windowunmap, windowactivate) on
// the session's window. No-op without X11 or xdotool; minimising still works then.
func x11Window(id, command string) {
	if os.Getenv("DISPLAY") == "" {
		return
	}
	if _, err := exec.LookPath("xdotool"); err != nil {
		return
	}
	_ = exec.Command("xdotool", "search", "--class", windowClass(id), command, "%@").Run()
}

// ensureSessionPreferences pre-grants notifications and sound to WhatsApp so the app
// window notifies without prompting, and keeps hidden tabs allowed.
func ensureSessionPreferences(sessionDir string) error {
	defaultDir := filepath.Join(sessionDir, "Default")
	if err := os.MkdirAll(defaultDir, 0700); err != nil {
		return err
	}
	prefsPath := filepath.Join(defaultDir, "Preferences")
	prefs := map[string]any{}
	if data, err := os.ReadFile(prefsPath); err == nil {
		_ = json.Unmarshal(data, &prefs)
	}
	// Chrome refuses hidden (windowless) tabs on a profile whose saved extension state
	// is loaded at startup ("only when remote debugging is enabled"). Extensions are
	// disabled anyway and Chrome rebuilds the built-in entries, so drop it.
	delete(child(prefs, "extensions"), "settings")
	exceptions := child(child(child(prefs, "profile"), "content_settings"), "exceptions")
	for _, k := range []string{"notifications", "sound"} {
		child(exceptions, k)["https://web.whatsapp.com:443,*"] = map[string]any{"setting": 1}
	}
	out, err := json.Marshal(prefs)
	if err != nil {
		return err
	}
	return os.WriteFile(prefsPath, out, 0600)
}

func child(m map[string]any, k string) map[string]any {
	c, ok := m[k].(map[string]any)
	if !ok {
		c = map[string]any{}
		m[k] = c
	}
	return c
}
