class ThemeService {
    private _current: 'dark' | 'light' = 'dark';

    get current(): 'dark' | 'light' {
        return this._current;
    }

    init(): void {
        const saved = localStorage.getItem('fleet-theme');
        this._current = saved === 'light' ? 'light' : 'dark';
        this._apply();
    }

    toggle(): void {
        this._current = this._current === 'dark' ? 'light' : 'dark';
        if (this._current === 'light') {
            localStorage.setItem('fleet-theme', 'light');
        } else {
            localStorage.removeItem('fleet-theme');
        }
        this._apply();
        window.dispatchEvent(
            new CustomEvent<{ theme: 'dark' | 'light' }>('theme-change', {
                detail: { theme: this._current },
            })
        );
    }

    private _apply(): void {
        if (this._current === 'light') {
            document.documentElement.dataset.theme = 'light';
        } else {
            delete document.documentElement.dataset.theme;
        }
    }
}

export const themeService = new ThemeService();
