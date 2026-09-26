import type { ReactiveController, ReactiveControllerHost } from 'lit';

interface ModalOptions {
    /** Close the modal the way its close button does. */
    close: () => void;
    /**
     * False while the close button is disabled — a job in flight that the
     * modal wants watched to the end. Escape follows the button. Default: true.
     */
    canClose?: () => boolean;
}

/** Open modals, innermost last. Only the innermost one answers the keyboard. */
const openModals: ModalController[] = [];

const FOCUSABLE = [
    'a[href]',
    'button:not([disabled])',
    'input:not([disabled]):not([type="hidden"])',
    'select:not([disabled])',
    'textarea:not([disabled])',
    '[tabindex]:not([tabindex="-1"])',
].join(',');

/** The focused element, looking into shadow roots. */
function deepActiveElement(): HTMLElement | null {
    let el = document.activeElement;
    while (el?.shadowRoot?.activeElement) el = el.shadowRoot.activeElement;
    return el instanceof HTMLElement ? el : null;
}

/**
 * Keyboard behaviour for a modal: Escape closes it, focus moves into it when
 * it opens, Tab stays inside it, and focus goes back to whatever opened it when
 * it closes.
 *
 * The modals here are mounted while open and unmounted when closed, so open
 * and close are the host's connect and disconnect. The host renders its dialog
 * as an element with role="dialog" (tabindex="-1", so it can hold focus); an
 * enabled element inside it marked `autofocus` gets focus instead, for a form
 * whose first field is the obvious place to start.
 */
export class ModalController implements ReactiveController {
    private opener: HTMLElement | null = null;
    private focusedOnOpen = false;
    private tabBackwards = false;

    constructor(
        private readonly host: ReactiveControllerHost & HTMLElement,
        private readonly options: ModalOptions,
    ) {
        host.addController(this);
    }

    hostConnected() {
        this.opener = deepActiveElement();
        this.focusedOnOpen = false;
        openModals.push(this);
        document.addEventListener('keydown', this.onKeydown);
        document.addEventListener('focusin', this.onFocusin);
    }

    hostDisconnected() {
        const i = openModals.indexOf(this);
        if (i >= 0) openModals.splice(i, 1);
        document.removeEventListener('keydown', this.onKeydown);
        document.removeEventListener('focusin', this.onFocusin);
        if (this.opener?.isConnected) this.opener.focus({ preventScroll: true });
        this.opener = null;
    }

    hostUpdated() {
        if (this.focusedOnOpen) return;
        this.focusedOnOpen = true;
        const root = this.host.shadowRoot;
        const target = root?.querySelector<HTMLElement>('[autofocus]:not([disabled])') ?? this.dialog();
        target?.focus({ preventScroll: true });
    }

    private dialog(): HTMLElement | null {
        return this.host.shadowRoot?.querySelector<HTMLElement>('[role="dialog"]') ?? null;
    }

    private get innermost(): boolean {
        return openModals[openModals.length - 1] === this;
    }

    private readonly onKeydown = (e: KeyboardEvent) => {
        if (!this.innermost) return;
        if (e.key === 'Escape') {
            // Handled here or refused here: either way no page-level Escape
            // handler behind the modal (a drawer, a search box) acts on it too.
            e.stopPropagation();
            if (this.options.canClose?.() ?? true) this.options.close();
        } else if (e.key === 'Tab') {
            this.tabBackwards = e.shiftKey;
            // Wrap at the ends. Left to the browser, Tab past the last control
            // goes on to the page behind or to the browser's own toolbar.
            const items = this.focusables();
            if (items.length === 0) return;
            const inside = this.host.shadowRoot?.activeElement ?? null;
            const edge = e.shiftKey ? items[0] : items[items.length - 1];
            if (inside === null || inside === edge) {
                e.preventDefault();
                (e.shiftKey ? items[items.length - 1] : items[0]).focus();
            }
        }
    };

    /**
     * Focus got out anyway (a click on the page behind, a control the Tab
     * handler didn't see): bring it back to the end it left from.
     */
    private readonly onFocusin = (e: FocusEvent) => {
        if (!this.innermost || e.composedPath().includes(this.host)) return;
        const items = this.focusables();
        const next = this.tabBackwards ? items[items.length - 1] : items[0];
        (next ?? this.dialog())?.focus();
    };

    /** The modal's own controls that can take focus right now. */
    private focusables(): HTMLElement[] {
        const all = this.host.shadowRoot?.querySelectorAll<HTMLElement>(FOCUSABLE) ?? [];
        return [...all].filter((el) => el.getClientRects().length > 0);
    }
}
