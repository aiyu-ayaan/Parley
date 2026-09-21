package backend

import (
	"bufio"
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
	"syscall"
	"time"
)

// session is one profile's supervised Chrome process. It always runs: headless in the
// background (forwarding notifications), or as an app window while the user has it open.
type session struct {
	cmd        *exec.Cmd
	window     bool // current process is the visible app window
	wantWindow bool // next launch should be the app window
	stopped    bool // user stopped it; supervisor exits
}

// notifyHook replaces the page's Notification API so every notification WhatsApp raises
// is handed to Go, and makes the page believe it is hidden so it notifies and never
// marks chats as read while nobody is looking.
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
  Object.defineProperty(Document.prototype, 'hidden', {get: () => true});
  Object.defineProperty(Document.prototype, 'visibilityState', {get: () => 'hidden'});
  Document.prototype.hasFocus = () => false;
})();`

func (a *App) sessionDir(id string) string {
	return filepath.Join(a.encryptionService.DataDir(), "sessions", id)
}

// startSession starts (or reuses) the supervisor for a profile.
func (a *App) startSession(id string, window bool) {
	a.procMu.Lock()
	defer a.procMu.Unlock()
	if a.shuttingDown {
		return
	}
	if s, ok := a.sessions[id]; ok {
		s.stopped = false
		if window && !s.window {
			s.wantWindow = true
			killGroup(s.cmd) // supervisor relaunches it as a window
		}
		return
	}
	a.sessions[id] = &session{wantWindow: window}
	go a.supervise(id)
}

// supervise keeps a profile's Chrome alive until it is stopped. When the window is
// closed, Chrome exits and the loop brings it straight back up headless.
func (a *App) supervise(id string) {
	for {
		a.procMu.Lock()
		s := a.sessions[id]
		if s == nil || s.stopped || a.shuttingDown {
			delete(a.sessions, id)
			a.procMu.Unlock()
			a.emitStatus(id)
			return
		}
		window := s.wantWindow
		s.wantWindow = false
		a.procMu.Unlock()

		cmd, err := a.launch(id, window)
		if err != nil {
			log.Printf("session %s: launch failed: %v", id, err)
			time.Sleep(5 * time.Second)
			continue
		}

		a.procMu.Lock()
		s.cmd, s.window = cmd, window
		a.procMu.Unlock()
		a.emitStatus(id)

		started := time.Now()
		_ = cmd.Wait()

		a.procMu.Lock()
		s.cmd, s.window = nil, false
		requested := s.wantWindow || s.stopped || a.shuttingDown
		a.procMu.Unlock()
		a.emitStatus(id)

		// Crash-loop guard: an unrequested exit this fast means Chrome can't start.
		if !requested && time.Since(started) < 3*time.Second {
			log.Printf("session %s: chrome exited after %s, backing off", id, time.Since(started).Round(time.Millisecond))
			time.Sleep(5 * time.Second)
		}
	}
}

// launch starts Chrome for the profile, either as an app window or headless with a
// DevTools pipe used to capture notifications.
func (a *App) launch(id string, window bool) (*exec.Cmd, error) {
	a.mu.RLock()
	p, ok := a.profiles[id]
	a.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("profile %s not found", id)
	}
	url := p.URL
	if url == "" {
		url = "https://web.whatsapp.com"
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
	}
	if window {
		cmd := exec.Command(chrome, append(args, "--app="+url, "--class=whatsweb-"+id)...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		return cmd, cmd.Start()
	}

	// --remote-debugging-pipe: Chrome reads commands on fd 3 and writes on fd 4.
	// No TCP port, so no other local process can drive the session.
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
	cmd := exec.Command(chrome, append(args, "--headless=new", "--remote-debugging-pipe", "--mute-audio")...)
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
	go func() {
		defer inW.Close()
		defer outR.Close()
		if err := a.watchNotifications(id, url, inW, outR); err != nil && !errors.Is(err, io.EOF) {
			log.Printf("session %s: devtools: %v", id, err)
		}
	}()
	return cmd, nil
}

// cdp is a minimal synchronous DevTools client over Chrome's NUL-delimited pipe.
type cdp struct {
	w      io.Writer
	r      *bufio.Reader
	nextID int
	onMsg  func(cdpMsg)
}

type cdpMsg struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *cdp) read() (cdpMsg, error) {
	var m cdpMsg
	b, err := c.r.ReadBytes(0)
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(b[:len(b)-1], &m)
}

// call sends a command and waits for its reply, passing any events seen meanwhile to onMsg.
func (c *cdp) call(session, method string, params any, out any) error {
	c.nextID++
	msg := map[string]any{"id": c.nextID, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if session != "" {
		msg["sessionId"] = session
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := c.w.Write(append(b, 0)); err != nil {
		return err
	}
	for {
		m, err := c.read()
		if err != nil {
			return err
		}
		if m.ID != c.nextID {
			c.onMsg(m)
			continue
		}
		if m.Error != nil {
			return fmt.Errorf("%s: %s", method, m.Error.Message)
		}
		if out != nil {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	}
}

// watchNotifications opens WhatsApp in the headless browser with the notification hook
// installed, then turns every hooked notification into a desktop notification.
func (a *App) watchNotifications(id, url string, w io.Writer, r io.Reader) error {
	c := &cdp{w: w, r: bufio.NewReader(r)}
	c.onMsg = func(m cdpMsg) {
		if m.Method != "Runtime.bindingCalled" {
			return
		}
		var ev struct{ Name, Payload string }
		var n struct{ Title, Body string }
		if json.Unmarshal(m.Params, &ev) == nil && ev.Name == "__wwNotify" && json.Unmarshal([]byte(ev.Payload), &n) == nil {
			go a.desktopNotify(id, n.Title, n.Body)
		}
	}

	var ver struct {
		UserAgent string `json:"userAgent"`
	}
	if err := c.call("", "Browser.getVersion", nil, &ver); err != nil {
		return err
	}
	var target struct {
		TargetID string `json:"targetId"`
	}
	if err := c.call("", "Target.createTarget", map[string]any{"url": "about:blank"}, &target); err != nil {
		return err
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := c.call("", "Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true}, &attached); err != nil {
		return err
	}
	s := attached.SessionID
	// WhatsApp refuses "HeadlessChrome", so present as the regular browser.
	ua := strings.ReplaceAll(ver.UserAgent, "HeadlessChrome", "Chrome")
	steps := []struct {
		method string
		params any
	}{
		{"Emulation.setUserAgentOverride", map[string]any{"userAgent": ua}},
		{"Runtime.addBinding", map[string]any{"name": "__wwNotify"}},
		{"Runtime.enable", nil},
		{"Page.enable", nil},
		{"Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": notifyHook}},
		{"Page.navigate", map[string]any{"url": url}},
	}
	for _, st := range steps {
		if err := c.call(s, st.method, st.params, nil); err != nil {
			return err
		}
	}
	for {
		m, err := c.read()
		if err != nil {
			return err
		}
		c.onMsg(m)
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

	out, err := exec.Command("notify-send", "-a", name, "-i", "whatsapp", "-u", "normal", "-A", "default=Open", title, body).Output()
	if err != nil && len(out) == 0 {
		// Older notify-send without actions.
		_ = exec.Command("notify-send", "-a", name, title, body).Run()
		return
	}
	if strings.TrimSpace(string(out)) == "default" {
		_ = a.OpenProfile(id)
	}
}

// evictStale stops a leftover Chrome (e.g. from an older Whatsweb run) that still owns
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

// focusWindow raises an already open profile window if a window tool is available.
func focusWindow(id string) {
	if _, err := exec.LookPath("xdotool"); err == nil {
		_ = exec.Command("xdotool", "search", "--classname", "whatsweb-"+id, "windowactivate").Run()
	} else if _, err := exec.LookPath("wmctrl"); err == nil {
		_ = exec.Command("wmctrl", "-x", "-a", "whatsweb-"+id).Run()
	}
}

// ensureSessionPreferences pre-grants notifications and sound to WhatsApp so the app
// window notifies without prompting.
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
