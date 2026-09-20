// Whatsweb Frontend Application

// Ensure compatibility between window.go.main and window.go.backend
function getBackendApp() {
    if (typeof window !== 'undefined' && window.go) {
        if (window.go.main && window.go.main.App) {
            if (!window.go.backend) window.go.backend = {};
            window.go.backend.App = window.go.main.App;
            return window.go.main.App;
        }
        if (window.go.backend && window.go.backend.App) {
            if (!window.go.main) window.go.main = {};
            window.go.main.App = window.go.backend.App;
            return window.go.backend.App;
        }
    }
    return null;
}

// Wait for backend bindings to be available
async function waitForBackend(timeoutMs = 3000) {
    const start = Date.now();
    while (Date.now() - start < timeoutMs) {
        const app = getBackendApp();
        if (app && app.CreateProfile) {
            return app;
        }
        await new Promise(resolve => setTimeout(resolve, 50));
    }
    return getBackendApp();
}

class WhatswebApp {
    constructor() {
        this.profiles = [];
        this.profileStatuses = {};
        this.activeProfileId = null;
        this.webview = null;
        this.statusInterval = null;

        // Bind events immediately so UI is always responsive
        this.bindEvents();
        this.init();
    }

    get api() {
        return getBackendApp();
    }

    async init() {
        const app = await waitForBackend(3000);
        if (app) {
            await this.loadProfiles();
            this.renderProfiles();
            await this.refreshStatuses();
            this.startStatusPolling();
            this.listenToBackendEvents();
        } else {
            // Keep retrying in background if startup was slow
            setTimeout(() => this.init(), 500);
        }
    }

    listenToBackendEvents() {
        if (typeof window !== 'undefined' && window.runtime && window.runtime.EventsOn) {
            window.runtime.EventsOn('profile:status-changed', (status) => {
                if (status && (status.id || status.ID)) {
                    const id = status.id || status.ID;
                    this.profileStatuses[id] = status;
                    this.updateAvatarBadge(status);
                    if (this.activeProfileId === id) {
                        this.updateDashboardState(status);
                    }
                } else {
                    this.refreshStatuses();
                }
            });
            window.runtime.EventsOn('profiles:updated', () => {
                this.loadProfiles().then(() => {
                    this.renderProfiles();
                    this.refreshStatuses();
                });
            });
        }
    }

    startStatusPolling() {
        if (this.statusInterval) clearInterval(this.statusInterval);
        this.statusInterval = setInterval(() => {
            this.refreshStatuses();
        }, 2000);
    }

    async refreshStatuses() {
        const api = this.api;
        if (!api) return;
        try {
            if (api.GetAllProfileStatuses) {
                const statuses = await api.GetAllProfileStatuses();
                if (Array.isArray(statuses)) {
                    statuses.forEach(s => {
                        const id = s.id || s.ID;
                        this.profileStatuses[id] = s;
                        this.updateAvatarBadge(s);
                    });
                    if (this.activeProfileId) {
                        const activeStatus = this.profileStatuses[this.activeProfileId];
                        if (activeStatus) {
                            this.updateDashboardState(activeStatus);
                        }
                    }
                }
            }
        } catch (e) {
            console.error('Failed to refresh statuses:', e);
        }
    }

    updateAvatarBadge(status) {
        const id = status.id || status.ID;
        const badge = document.getElementById(`badge-${id}`);
        if (!badge) return;

        const isRunning = status.isRunning !== undefined ? status.isRunning : status.IsRunning;
        const hasWindow = status.hasWindow !== undefined ? status.hasWindow : status.HasWindow;

        if (isRunning) {
            badge.className = 'profile-badge ' + (hasWindow ? 'active-window' : 'running-bg');
            badge.title = hasWindow ? 'Window Active' : 'Running in Background (Notifications Active)';
        } else {
            badge.className = 'profile-badge';
            badge.title = 'Offline';
        }
    }

    updateDashboardState(status) {
        const indicator = document.getElementById('dashboardStatusIndicator');
        const statusText = document.getElementById('dashboardStatusText');
        const launchBtnText = document.getElementById('launchBtnText');
        const bgNotice = document.getElementById('dashboardBgNotice');
        const hideBtn = document.getElementById('hideWhatsappBtn');
        const stopBtn = document.getElementById('stopWhatsappBtn');

        const isRunning = status ? (status.isRunning !== undefined ? status.isRunning : status.IsRunning) : false;
        const hasWindow = status ? (status.hasWindow !== undefined ? status.hasWindow : status.HasWindow) : false;

        if (isRunning && hasWindow) {
            if (indicator) indicator.className = 'status-indicator active-window';
            if (statusText) statusText.textContent = 'Active (Window Open)';
            if (launchBtnText) launchBtnText.textContent = 'Bring Window to Front';
            if (bgNotice) bgNotice.style.display = 'none';
            if (hideBtn) hideBtn.style.display = 'inline-flex';
            if (stopBtn) stopBtn.style.display = 'inline-flex';
        } else if (isRunning && !hasWindow) {
            if (indicator) indicator.className = 'status-indicator running-bg';
            if (statusText) statusText.textContent = 'Running in Background';
            if (launchBtnText) launchBtnText.textContent = 'Open WhatsApp Window';
            if (bgNotice) bgNotice.style.display = 'flex';
            if (hideBtn) hideBtn.style.display = 'none';
            if (stopBtn) stopBtn.style.display = 'inline-flex';
        } else {
            if (indicator) indicator.className = 'status-indicator';
            if (statusText) statusText.textContent = 'Ready to Launch';
            if (launchBtnText) launchBtnText.textContent = 'Open WhatsApp Web';
            if (bgNotice) bgNotice.style.display = 'none';
            if (hideBtn) hideBtn.style.display = 'none';
            if (stopBtn) stopBtn.style.display = 'none';
        }
    }

    bindEvents() {
        // Add profile buttons
        const addBtn = document.getElementById('addProfileBtn');
        if (addBtn) {
            addBtn.addEventListener('click', () => this.openModal());
        }

        const welcomeBtn = document.getElementById('welcomeAddBtn');
        if (welcomeBtn) {
            welcomeBtn.addEventListener('click', () => this.openModal());
        }

        // Modal events
        const modalClose = document.getElementById('modalClose');
        if (modalClose) {
            modalClose.addEventListener('click', () => this.closeModal());
        }

        const cancelBtn = document.getElementById('cancelBtn');
        if (cancelBtn) {
            cancelBtn.addEventListener('click', () => this.closeModal());
        }

        const modalOverlay = document.getElementById('modalOverlay');
        if (modalOverlay) {
            modalOverlay.addEventListener('click', (e) => {
                if (e.target === modalOverlay) {
                    this.closeModal();
                }
            });
        }

        // Form submission and Create Profile button
        const form = document.getElementById('addProfileForm');
        if (form) {
            form.addEventListener('submit', (e) => this.handleCreateProfile(e));
        }

        const submitBtn = document.getElementById('createProfileSubmitBtn');
        if (submitBtn) {
            submitBtn.addEventListener('click', (e) => {
                if (form && !form.checkValidity()) {
                    form.reportValidity();
                    return;
                }
                this.handleCreateProfile(e);
            });
        }

        // Launch / Bring to front WhatsApp Web button
        const launchBtn = document.getElementById('launchWhatsappBtn');
        if (launchBtn) {
            launchBtn.addEventListener('click', () => {
                if (this.activeProfileId) {
                    const api = this.api;
                    if (api && api.OpenProfile) {
                        const statusText = document.getElementById('dashboardStatusText');
                        if (statusText) statusText.textContent = 'Opening Window...';
                        api.OpenProfile(this.activeProfileId).then(() => {
                            this.refreshStatuses();
                        }).catch(err => {
                            console.error('Failed to open profile:', err);
                            this.refreshStatuses();
                        });
                    }
                }
            });
        }

        // Send to background button
        const hideBtn = document.getElementById('hideWhatsappBtn');
        if (hideBtn) {
            hideBtn.addEventListener('click', () => {
                if (this.activeProfileId) {
                    const api = this.api;
                    if (api && api.HideProfileWindow) {
                        api.HideProfileWindow(this.activeProfileId).then(() => {
                            this.refreshStatuses();
                        }).catch(console.error);
                    }
                }
            });
        }

        // Stop session button
        const stopBtn = document.getElementById('stopWhatsappBtn');
        if (stopBtn) {
            stopBtn.addEventListener('click', () => {
                if (this.activeProfileId) {
                    if (confirm('Stop this WhatsApp background session? You will stop receiving notifications until you start it again.')) {
                        const api = this.api;
                        if (api && api.CloseProfile) {
                            api.CloseProfile(this.activeProfileId).then(() => {
                                this.refreshStatuses();
                            }).catch(console.error);
                        }
                    }
                }
            });
        }

        // Webview controls
        const closeBtn = document.getElementById('closeBtn');
        if (closeBtn) {
            closeBtn.addEventListener('click', () => this.closeWebview());
        }

        const minimizeBtn = document.getElementById('minimizeBtn');
        if (minimizeBtn) {
            minimizeBtn.addEventListener('click', () => this.minimizeApp());
        }
    }

    async loadProfiles() {
        try {
            const api = this.api;
            if (api && api.GetProfilesDTO) {
                const result = await api.GetProfilesDTO();
                this.profiles = (result || []).map(p => ({
                    id: p.id || p.ID,
                    name: p.name || p.Name,
                    url: p.url || p.URL || 'https://web.whatsapp.com'
                }));
            }
        } catch (err) {
            console.error('Failed to load profiles:', err);
            this.profiles = [];
        }
    }

    renderProfiles() {
        const container = document.getElementById('profilesList');
        if (!container) return;
        container.innerHTML = '';

        this.profiles.forEach(profile => {
            const item = this.createProfileElement(profile);
            container.appendChild(item);
        });
    }

    createProfileElement(profile) {
        const div = document.createElement('div');
        div.className = 'profile-item' + (profile.id === this.activeProfileId ? ' active' : '');
        div.dataset.profileId = profile.id;

        const initial = (profile.name || '?').charAt(0).toUpperCase();

        div.innerHTML = `
            <div class="profile-avatar">${initial}</div>
            <div class="profile-badge" id="badge-${profile.id}" title="Offline"></div>
            <div class="profile-tooltip">${this.escapeHtml(profile.name)}</div>
        `;

        div.addEventListener('click', () => this.selectProfile(profile.id));
        div.addEventListener('contextmenu', (e) => this.showProfileContextMenu(e, profile));

        return div;
    }

    selectProfile(profileId) {
        this.activeProfileId = profileId;

        // Update UI
        document.querySelectorAll('.profile-item').forEach(item => {
            item.classList.toggle('active', item.dataset.profileId === profileId);
        });

        // Open in dashboard
        const profile = this.profiles.find(p => p.id === profileId);
        if (profile) {
            this.openWebview(profile);
        }
    }

    openWebview(profile) {
        // Hide welcome screen, show webview container
        const welcomeScreen = document.getElementById('welcomeScreen');
        const webviewContainer = document.getElementById('webviewContainer');
        if (welcomeScreen) welcomeScreen.style.display = 'none';
        if (webviewContainer) webviewContainer.style.display = 'flex';

        // Update header
        const initial = (profile.name || '?').charAt(0).toUpperCase();

        const profileNameEl = document.getElementById('webviewProfileName');
        if (profileNameEl) profileNameEl.textContent = profile.name;

        const avatarEl = document.getElementById('webviewAvatar');
        if (avatarEl) avatarEl.textContent = initial;

        // Update dashboard elements
        const dashAvatarEl = document.getElementById('dashboardAvatar');
        if (dashAvatarEl) dashAvatarEl.textContent = initial;

        const dashNameEl = document.getElementById('dashboardProfileName');
        if (dashNameEl) dashNameEl.textContent = profile.name;

        // Update status immediately from cached state if available
        const currentStatus = this.profileStatuses[profile.id];
        this.updateDashboardState(currentStatus);

        // Fetch fresh status and if not running, launch
        const api = this.api;
        if (api) {
            if (api.GetProfileStatus) {
                api.GetProfileStatus(profile.id).then(status => {
                    this.profileStatuses[profile.id] = status;
                    this.updateAvatarBadge(status);
                    this.updateDashboardState(status);

                    // If not running at all, launch it
                    const isRunning = status.isRunning !== undefined ? status.isRunning : status.IsRunning;
                    if (!isRunning && api.OpenProfile) {
                        const statusText = document.getElementById('dashboardStatusText');
                        if (statusText) statusText.textContent = 'Launching WhatsApp Web...';
                        api.OpenProfile(profile.id).then(() => {
                            this.refreshStatuses();
                        }).catch(console.error);
                    }
                }).catch(console.error);
            } else if (api.OpenProfile) {
                api.OpenProfile(profile.id).catch(console.error);
            }
        }
    }

    closeWebview() {
        const welcomeScreen = document.getElementById('welcomeScreen');
        const webviewContainer = document.getElementById('webviewContainer');
        if (welcomeScreen) welcomeScreen.style.display = 'flex';
        if (webviewContainer) webviewContainer.style.display = 'none';
        this.activeProfileId = null;
        document.querySelectorAll('.profile-item').forEach(item => item.classList.remove('active'));
    }

    minimizeApp() {
        const api = this.api;
        if (api && api.Minimize) {
            api.Minimize();
        }
    }

    openModal() {
        const modalOverlay = document.getElementById('modalOverlay');
        if (modalOverlay) modalOverlay.style.display = 'flex';
        const profileName = document.getElementById('profileName');
        if (profileName) {
            profileName.focus();
            profileName.select();
        }
    }

    closeModal() {
        const modalOverlay = document.getElementById('modalOverlay');
        if (modalOverlay) modalOverlay.style.display = 'none';
        const form = document.getElementById('addProfileForm');
        if (form) form.reset();
    }

    async handleCreateProfile(e) {
        if (e && e.preventDefault) {
            e.preventDefault();
        }

        const nameInput = document.getElementById('profileName');
        const name = nameInput ? nameInput.value.trim() : '';
        if (!name) {
            if (nameInput) nameInput.focus();
            return;
        }

        const submitBtn = document.getElementById('createProfileSubmitBtn');
        if (submitBtn) {
            submitBtn.disabled = true;
            submitBtn.textContent = 'Creating...';
        }

        try {
            const api = await waitForBackend(2000);
            if (!api || !api.CreateProfile) {
                throw new Error('Backend API is not available');
            }

            const profile = await api.CreateProfile(name);
            const newProfile = {
                id: profile.id || profile.ID,
                name: profile.name || profile.Name,
                url: profile.url || profile.URL || 'https://web.whatsapp.com'
            };

            // Avoid duplicate additions
            if (!this.profiles.some(p => p.id === newProfile.id)) {
                this.profiles.push(newProfile);
            }
            this.renderProfiles();
            this.closeModal();
            this.selectProfile(newProfile.id);
        } catch (err) {
            console.error('Failed to create profile:', err);
            alert('Failed to create profile: ' + (err.message || err));
        } finally {
            if (submitBtn) {
                submitBtn.disabled = false;
                submitBtn.textContent = 'Create Profile';
            }
        }
    }

    showProfileContextMenu(e, profile) {
        e.preventDefault();

        // Remove existing context menu
        const existing = document.querySelector('.context-menu');
        if (existing) existing.remove();

        const menu = document.createElement('div');
        menu.className = 'context-menu';
        menu.style.cssText = `
            position: fixed;
            top: ${e.clientY}px;
            left: ${e.clientX}px;
            background: var(--bg-tertiary);
            border: 1px solid var(--border-color);
            border-radius: 8px;
            padding: 8px 0;
            z-index: 2000;
            min-width: 160px;
            box-shadow: 0 4px 20px rgba(0,0,0,0.3);
        `;

        menu.innerHTML = `
            <div class="context-menu-item" data-action="rename" style="padding: 10px 16px; cursor: pointer; transition: background 0.1s;">Rename</div>
            <div class="context-menu-item" data-action="delete" style="padding: 10px 16px; cursor: pointer; transition: background 0.1s; color: #ff6b6b;">Delete</div>
        `;

        menu.querySelectorAll('.context-menu-item').forEach(item => {
            item.addEventListener('mouseenter', () => item.style.background = 'var(--bg-secondary)');
            item.addEventListener('mouseleave', () => item.style.background = 'transparent');
            item.addEventListener('click', () => this.handleContextAction(item.dataset.action, profile, menu));
        });

        document.body.appendChild(menu);

        // Close on click outside
        const closeMenu = (ev) => {
            if (!menu.contains(ev.target)) {
                menu.remove();
                document.removeEventListener('click', closeMenu);
            }
        };
        setTimeout(() => document.addEventListener('click', closeMenu), 0);
    }

    async handleContextAction(action, profile, menu) {
        menu.remove();

        switch (action) {
            case 'rename':
                const newName = prompt('Enter new name:', profile.name);
                if (newName && newName.trim() !== profile.name) {
                    await this.renameProfile(profile.id, newName.trim());
                }
                break;
            case 'delete':
                if (confirm(`Delete profile "${profile.name}"?`)) {
                    await this.deleteProfile(profile.id);
                }
                break;
        }
    }

    async renameProfile(id, newName) {
        try {
            const profile = this.profiles.find(p => p.id === id);
            const api = this.api;
            if (profile && api && api.UpdateProfile) {
                profile.name = newName;
                await api.UpdateProfile({
                    id: profile.id,
                    name: profile.name,
                    url: profile.url || 'https://web.whatsapp.com'
                });
                this.renderProfiles();
                if (this.activeProfileId === id) {
                    const profileNameEl = document.getElementById('webviewProfileName');
                    if (profileNameEl) profileNameEl.textContent = newName;
                    const avatarEl = document.getElementById('webviewAvatar');
                    if (avatarEl) avatarEl.textContent = newName.charAt(0).toUpperCase();
                }
            }
        } catch (err) {
            console.error('Failed to rename profile:', err);
            alert('Failed to rename profile: ' + (err.message || err));
        }
    }

    async deleteProfile(id) {
        try {
            const api = this.api;
            if (api && api.DeleteProfile) {
                await api.DeleteProfile(id);
            }
            this.profiles = this.profiles.filter(p => p.id !== id);
            this.renderProfiles();

            if (this.activeProfileId === id) {
                this.closeWebview();
            }
        } catch (err) {
            console.error('Failed to delete profile:', err);
            alert('Failed to delete profile: ' + (err.message || err));
        }
    }

    escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }
}

// Initialize app when DOM is ready
function startApp() {
    window.whatsweb = new WhatswebApp();
}

if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', startApp);
} else {
    startApp();
}

// Listen for profile updates from backend
function setupRuntimeEvents() {
    const wailsRuntime = window.runtime || (typeof runtime !== 'undefined' ? runtime : null);
    if (wailsRuntime && wailsRuntime.EventsOn) {
        wailsRuntime.EventsOn('profiles:updated', (profiles) => {
            if (window.whatsweb) {
                window.whatsweb.profiles = (profiles || []).map(p => ({
                    id: p.id || p.ID,
                    name: p.name || p.Name,
                    url: p.url || p.URL || 'https://web.whatsapp.com'
                }));
                window.whatsweb.renderProfiles();
            }
        });

        wailsRuntime.EventsOn('profile:open', (profileId) => {
            if (window.whatsweb && window.whatsweb.activeProfileId === profileId) {
                const statusText = document.getElementById('dashboardStatusText');
                if (statusText) statusText.textContent = 'Session Active (Window Open)';
            }
        });

        wailsRuntime.EventsOn('profile:closed', (profileId) => {
            if (window.whatsweb && window.whatsweb.activeProfileId === profileId) {
                const statusText = document.getElementById('dashboardStatusText');
                if (statusText) statusText.textContent = 'Ready to Launch';
            }
        });
    } else {
        setTimeout(setupRuntimeEvents, 200);
    }
}
setupRuntimeEvents();
