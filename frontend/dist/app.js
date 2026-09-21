// Parley dashboard

const $ = (id) => document.getElementById(id);
const api = () => window.go && window.go.backend && window.go.backend.App;

const state = {
    profiles: [],
    status: {},      // id -> {isRunning, hasWindow}
    selected: null,  // profile id, or 'settings'
};

const COLORS = ['#00a884', '#53bdeb', '#a970ff', '#f7a541', '#e26ab6', '#06cf9c', '#ff7a6b', '#6b8afd'];

function colorFor(id) {
    let h = 0;
    for (const c of id) h = (h * 31 + c.charCodeAt(0)) | 0;
    return COLORS[Math.abs(h) % COLORS.length];
}

function initials(name) {
    return name.trim().split(/\s+/).slice(0, 2).map((w) => w[0]).join('').toUpperCase() || '?';
}

function mode(id) {
    const s = state.status[id];
    if (!s || !s.isRunning) return 'stopped';
    return s.hasWindow ? 'window' : 'background';
}

const MODE_TEXT = {
    window: ['Window open', 'WhatsApp is open in its own window.'],
    background: ['Background', 'Running headless. Message and call notifications are on.'],
    stopped: ['Stopped', 'Not running. You won\'t get notifications for this account.'],
};

function renderRail() {
    const list = $('profileList');
    list.replaceChildren(...state.profiles.map((p) => {
        const b = document.createElement('button');
        b.className = 'orb' + (state.selected === p.id ? ' selected' : '');
        b.style.background = colorFor(p.id);
        b.title = `${p.name} · ${MODE_TEXT[mode(p.id)][0]}`;
        b.setAttribute('aria-label', b.title);
        b.textContent = initials(p.name);
        b.onclick = () => select(p.id);
        b.ondblclick = () => api().OpenProfile(p.id);
        const dot = document.createElement('span');
        dot.className = 'dot ' + mode(p.id);
        b.append(dot);
        return b;
    }));
    $('homeBtn').classList.toggle('selected', state.selected === 'settings');
}

function renderMain() {
    const settings = state.selected === 'settings';
    const p = state.profiles.find((x) => x.id === state.selected);
    $('settingsView').hidden = !settings;
    $('profileView').hidden = !p;
    $('emptyView').hidden = settings || !!p;
    if (!p) return;

    const m = mode(p.id);
    $('barName').textContent = p.name;
    $('statusChip').textContent = MODE_TEXT[m][0];
    $('statusChip').className = 'chip ' + m;
    $('statusText').textContent = MODE_TEXT[m][1];

    const av = $('heroAvatar');
    av.textContent = initials(p.name);
    av.style.background = colorFor(p.id);
    if (document.activeElement !== $('nameInput')) $('nameInput').value = p.name;

    $('openBtn').textContent = m === 'window' ? 'Focus window' : 'Open WhatsApp';
    $('bgBtn').hidden = m !== 'window';
    $('powerBtn').textContent = m === 'stopped' ? 'Start in background' : 'Stop';
    resetDelete();
}

function render() {
    renderRail();
    renderMain();
}

function select(id) {
    state.selected = id;
    render();
}

async function loadProfiles() {
    const list = (await api().GetProfiles()) || [];
    state.profiles = list.sort((a, b) => a.name.localeCompare(b.name));
    if (state.selected !== 'settings' && !state.profiles.some((p) => p.id === state.selected)) {
        state.selected = state.profiles.length ? state.profiles[0].id : null;
    }
}

async function loadStatuses() {
    for (const s of (await api().GetAllProfileStatuses()) || []) state.status[s.id] = s;
}

async function refresh() {
    await loadProfiles();
    await loadStatuses();
    render();
}

// Delete asks for a second click instead of a blocking confirm().
function resetDelete() {
    const b = $('deleteBtn');
    b.classList.remove('confirm');
    b.textContent = 'Remove account';
}

function bind() {
    const current = () => state.selected;

    $('homeBtn').onclick = () => select('settings');
    $('addBtn').onclick = $('emptyAddBtn').onclick = () => {
        $('addName').value = '';
        $('addDialog').showModal();
    };
    $('addDialog').addEventListener('close', async () => {
        const name = $('addName').value.trim();
        if ($('addDialog').returnValue !== 'ok' || !name) return;
        const p = await api().CreateProfile(name);
        state.selected = p.id;
        await refresh();
        await api().OpenProfile(p.id);
    });

    $('openBtn').onclick = () => api().OpenProfile(current());
    $('bgBtn').onclick = () => api().HideProfileWindow(current());
    $('powerBtn').onclick = () => (mode(current()) === 'stopped' ? api().StartProfile(current()) : api().CloseProfile(current()));

    $('nameInput').addEventListener('keydown', (e) => {
        if (e.key === 'Enter') e.target.blur();
        if (e.key === 'Escape') { e.target.value = ''; e.target.blur(); }
    });
    $('nameInput').addEventListener('blur', async (e) => {
        const p = state.profiles.find((x) => x.id === current());
        const name = e.target.value.trim();
        if (!p || !name || name === p.name) { renderMain(); return; }
        await api().UpdateProfile({ ...p, name });
    });

    $('deleteBtn').onclick = async (e) => {
        const b = e.currentTarget;
        if (!b.classList.contains('confirm')) {
            b.classList.add('confirm');
            b.textContent = 'Click again to remove (logs out and deletes data)';
            return;
        }
        await api().DeleteProfile(current());
        await refresh();
    };

    $('autostartToggle').onchange = (e) => api().SetAutoStart(e.target.checked);
    $('quitBtn').onclick = () => api().Quit();
}

async function start() {
    if (!api() || !window.runtime) {
        setTimeout(start, 50);
        return;
    }
    window.runtime.EventsOn('profiles:updated', refresh);
    window.runtime.EventsOn('profile:status-changed', (s) => {
        state.status[s.id] = s;
        render();
    });
    $('autostartToggle').checked = await api().IsAutoStart();
    await refresh();
}

bind();
start();
