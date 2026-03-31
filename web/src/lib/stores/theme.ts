import { writable } from 'svelte/store';

export type Theme = 'light' | 'dark' | 'system';

const STORAGE_KEY = 'onscreen-theme';

function getSystemTheme(): 'light' | 'dark' {
	if (typeof window === 'undefined') return 'dark';
	return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
}

function getInitialTheme(): Theme {
	if (typeof window === 'undefined') return 'system';
	return (localStorage.getItem(STORAGE_KEY) as Theme) ?? 'system';
}

function applyTheme(theme: Theme) {
	if (typeof document === 'undefined') return;
	const resolved = theme === 'system' ? getSystemTheme() : theme;
	document.documentElement.dataset.theme = resolved;
}

function createThemeStore() {
	const { subscribe, set, update } = writable<Theme>(getInitialTheme());

	return {
		subscribe,
		set(value: Theme) {
			if (typeof window !== 'undefined') {
				localStorage.setItem(STORAGE_KEY, value);
			}
			applyTheme(value);
			set(value);
		},
		toggle() {
			update((current) => {
				const resolved = current === 'system' ? getSystemTheme() : current;
				const next: Theme = resolved === 'dark' ? 'light' : 'dark';
				if (typeof window !== 'undefined') {
					localStorage.setItem(STORAGE_KEY, next);
				}
				applyTheme(next);
				return next;
			});
		},
		init() {
			const theme = getInitialTheme();
			applyTheme(theme);

			// Listen for system theme changes when in 'system' mode.
			if (typeof window !== 'undefined') {
				window.matchMedia('(prefers-color-scheme: light)').addEventListener('change', () => {
					const current = localStorage.getItem(STORAGE_KEY) as Theme ?? 'system';
					if (current === 'system') {
						applyTheme('system');
					}
				});
			}
		}
	};
}

export const theme = createThemeStore();
