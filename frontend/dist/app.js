// Whatsweb Frontend Application

class WhatswebApp {
    constructor() {
        this.profiles = [];
        this.activeProfileId = null;
        this.webview = null;
        
        this.init();
    }

    async init() {
        // Wait for Wails to be ready
        if (typeof window.go === 'undefined') {
            // Wait a bit for Wails to initialize
            setTimeout(() => this.init(), 100);
            return;
        }

        this.bindEvents();
        await this.loadProfiles();
        this.renderProfiles();
    }

    bindEvents() {
        // Add profile button
        document.getElementById('addProfileBtn').addEventListener('click', () => this.openModal());
        document.getElementById('welcomeAddBtn').addEventListener('click', () => this.openModal());
        
        // Modal events
        document.getElementById('modalClose').addEventListener('click', () => this.closeModal());
        document.getElementById('cancelBtn').addEventListener('click', () => this.closeModal());
        document.getElementById('modalOverlay').addEventListener('click', (e) => {
            if (e.target === document.getElementById('modalOverlay')) {
                this.closeModal();
            }
        });
        
        // Form submission
        document.getElementById('addProfileForm').addEventListener('submit', (e) => this.handleCreateProfile(e));
        
        // Webview controls
        document.getElementById('closeBtn').addEventListener('click', () => this.closeWebview());
        document.getElementById('minimizeBtn').addEventListener('click', () => this.minimizeApp());
        
        // Listen for backend events
        if (window.go && window.go.main && window.go.main.App) {
            // Events will be handled via runtime.EventsOn in Go
        }
    }

    async loadProfiles() {
        try {
            const result = await window.go.main.App.GetProfilesDTO();
            this.profiles = result || [];
        } catch (err) {
            console.error('Failed to load profiles:', err);
            this.profiles = [];
        }
    }

    renderProfiles() {
        const container = document.getElementById('profilesList');
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
        
        const initial = profile.name.charAt(0).toUpperCase();
        
        div.innerHTML = `
            <div class="profile-avatar">${initial}</div>
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
        
        // Open in webview
        const profile = this.profiles.find(p => p.id === profileId);
        if (profile) {
            this.openWebview(profile);
        }
    }

    openWebview(profile) {
        // Hide welcome screen, show webview
        document.getElementById('welcomeScreen').style.display = 'none';
        document.getElementById('webviewContainer').style.display = 'flex';
        
        // Update header
        document.getElementById('webviewProfileName').textContent = profile.name;
        const avatar = document.getElementById('webviewAvatar');
        avatar.textContent = profile.name.charAt(0).toUpperCase();
        
        // Load webview
        this.webview = document.getElementById('whatsappWebview');
        this.webview.src = profile.url;
        
        // Notify backend
        window.go.main.App.OpenProfile(profile.id);
    }

    closeWebview() {
        document.getElementById('welcomeScreen').style.display = 'flex';
        document.getElementById('webviewContainer').style.display = 'none';
        this.activeProfileId = null;
        document.querySelectorAll('.profile-item').forEach(item => item.classList.remove('active'));
    }

    minimizeApp() {
        if (window.go && window.go.main && window.go.main.App) {
            window.go.main.App.Minimize();
        }
    }

    openModal() {
        document.getElementById('modalOverlay').style.display = 'flex';
        document.getElementById('profileName').focus();
    }

    closeModal() {
        document.getElementById('modalOverlay').style.display = 'none';
        document.getElementById('addProfileForm').reset();
    }

    async handleCreateProfile(e) {
        e.preventDefault();
        
        const name = document.getElementById('profileName').value.trim();
        if (!name) return;
        
        try {
            const profile = await window.go.main.App.CreateProfile(name);
            this.profiles.push({ id: profile.id, name: profile.name });
            this.renderProfiles();
            this.closeModal();
            this.selectProfile(profile.id);
        } catch (err) {
            console.error('Failed to create profile:', err);
            alert('Failed to create profile: ' + err.message);
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
        const closeMenu = (e) => {
            if (!menu.contains(e.target)) {
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
            if (profile) {
                profile.name = newName;
                await window.go.main.App.UpdateProfile(profile);
                this.renderProfiles();
                if (this.activeProfileId === id) {
                    document.getElementById('webviewProfileName').textContent = newName;
                    document.getElementById('webviewAvatar').textContent = newName.charAt(0).toUpperCase();
                }
            }
        } catch (err) {
            console.error('Failed to rename profile:', err);
            alert('Failed to rename profile: ' + err.message);
        }
    }

    async deleteProfile(id) {
        try {
            await window.go.main.App.DeleteProfile(id);
            this.profiles = this.profiles.filter(p => p.id !== id);
            this.renderProfiles();
            
            if (this.activeProfileId === id) {
                this.closeWebview();
            }
        } catch (err) {
            console.error('Failed to delete profile:', err);
            alert('Failed to delete profile: ' + err.message);
        }
    }

    escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }
}

// Initialize app when DOM is ready
document.addEventListener('DOMContentLoaded', () => {
    window.whatsweb = new WhatswebApp();
});

// Listen for profile updates from backend
if (typeof runtime !== 'undefined') {
    runtime.EventsOn('profiles:updated', (profiles) => {
        if (window.whatsweb) {
            window.whatsweb.profiles = profiles.map(p => ({ id: p.ID, name: p.Name }));
            window.whatsweb.renderProfiles();
        }
    });
}
