// Simple client-side router
import { state } from './state.js';

const VIEW_STORAGE_KEY = 'currentView';

class Router {
    constructor() {
        this.routes = new Map();
        this.currentView = null;
    }

    register(name, component) {
        this.routes.set(name, component);
    }

    navigate(viewName) {
        if (!this.routes.has(viewName)) {
            console.error(`View "${viewName}" not found`);
            return;
        }

        // Update active nav link
        document.querySelectorAll('.nav-link').forEach(link => {
            link.classList.remove('active');
            if (link.dataset.view === viewName) {
                link.classList.add('active');
            }
        });

        // Update state
        state.update('currentView', viewName);
        try {
            localStorage.setItem(VIEW_STORAGE_KEY, viewName);
        } catch (e) {
            // Ignore quota / privacy-mode errors — persistence is best-effort.
        }

        // Render view
        const component = this.routes.get(viewName);
        this.currentView = component;
        component.render();
    }

    init() {
        // Set up nav link click handlers
        document.querySelectorAll('.nav-link').forEach(link => {
            link.addEventListener('click', (e) => {
                e.preventDefault();
                const viewName = link.dataset.view;
                this.navigate(viewName);
            });
        });

        // Restore the last visited view from localStorage; fall back to the
        // default in state if storage is empty or holds an unknown view.
        let initialView = state.get('currentView');
        try {
            const saved = localStorage.getItem(VIEW_STORAGE_KEY);
            if (saved && this.routes.has(saved)) {
                initialView = saved;
            }
        } catch (e) {
            // Ignore — fall back to default.
        }
        this.navigate(initialView);
    }
}

export const router = new Router();
