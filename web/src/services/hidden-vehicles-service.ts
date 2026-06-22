const STORAGE_KEY_PREFIX = 'fleet-lite:hidden:';
const EVENT_NAME = 'fleet-lite-hidden-changed';

class HiddenVehiclesService {
    private storageKey(tenantId: string): string {
        return `${STORAGE_KEY_PREFIX}${tenantId}`;
    }

    getHidden(tenantId: string): Set<string> {
        try {
            const raw = localStorage.getItem(this.storageKey(tenantId));
            const arr: unknown = raw ? JSON.parse(raw) : [];
            return new Set(Array.isArray(arr) ? (arr as string[]) : []);
        } catch {
            return new Set();
        }
    }

    isHidden(tenantId: string, tokenId: string): boolean {
        return this.getHidden(tenantId).has(tokenId);
    }

    hide(tenantId: string, tokenId: string): void {
        const set = this.getHidden(tenantId);
        set.add(tokenId);
        localStorage.setItem(this.storageKey(tenantId), JSON.stringify([...set]));
        window.dispatchEvent(new CustomEvent(EVENT_NAME));
    }

    unhide(tenantId: string, tokenId: string): void {
        const set = this.getHidden(tenantId);
        set.delete(tokenId);
        localStorage.setItem(this.storageKey(tenantId), JSON.stringify([...set]));
        window.dispatchEvent(new CustomEvent(EVENT_NAME));
    }

    subscribe(cb: () => void): () => void {
        const h = () => cb();
        window.addEventListener(EVENT_NAME, h);
        return () => window.removeEventListener(EVENT_NAME, h);
    }
}

export const hiddenVehiclesService = new HiddenVehiclesService();
