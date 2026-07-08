// Entry script for accept-invite.html — the landing page for an emailed invite
// link ({APP_BASE_URL}/accept-invite.html?token=...). It stashes the token so it
// survives the DIMO login round-trip (login redirects to /login.html, which —
// when a pending token is set — bounces back here), then — once signed in —
// shows WHO the invite will bind to and waits for an explicit confirm. The
// confirm step exists because the token is single-use: silently accepting with
// whatever session happened to be active once consumed an invite under the
// wrong (shared) account, locking the real invitee out.
import './elements/login-element.ts'; // registers <login-element>
import { getTokenClaims, isTokenExpired } from './utils/token.ts';
import { TenantService } from './services/tenant-service.ts';
import { ApiError } from './services/api-service.ts';

const PENDING_KEY = 'pendingInviteToken';

function el(id: string): HTMLElement | null {
    return document.getElementById(id);
}

function show(view: 'loading' | 'login' | 'confirm' | 'error' | 'success', message?: string) {
    for (const v of ['loading', 'login', 'confirm', 'error', 'success']) {
        const node = el(`view-${v}`);
        if (node) node.style.display = v === view ? 'block' : 'none';
    }
    if (message) {
        const msgNode = el(`${view}-message`);
        if (msgNode) msgNode.textContent = message;
    }
}

// API errors arrive as a JSON body string (`{"code":410,"message":"…"}`).
function extractMessage(err: unknown): string {
    const raw = err instanceof ApiError ? err.message : err instanceof Error ? err.message : '';
    try {
        const parsed = JSON.parse(raw);
        if (parsed && typeof parsed.message === 'string') return parsed.message;
    } catch {
        // not JSON — fall through
    }
    return raw;
}

async function run() {
    const params = new URLSearchParams(location.search);
    const queryToken = params.get('token') ?? '';
    let token = queryToken;
    if (token) {
        localStorage.setItem(PENDING_KEY, token);
    } else {
        token = localStorage.getItem(PENDING_KEY) ?? '';
    }

    if (!token) {
        show('error', 'This invitation link is missing its token. Ask the sender to re-send the invite.');
        return;
    }

    // Not signed in: show the DIMO login button. After sign-in the user returns
    // via /login.html, which forwards back here (pending token set) and lands on
    // the confirm step below.
    if (isTokenExpired(localStorage.getItem('token'))) {
        show('login');
        return;
    }

    // Signed in: show who the invite will bind to and wait for an explicit
    // confirm — the token is single-use, so consuming it under whatever session
    // happens to be active is how invites get burned by the wrong account.
    const identity = el('confirm-identity');
    if (identity) identity.textContent = signedInIdentity();
    el('confirm-accept')?.addEventListener('click', () => void accept(token));
    el('confirm-switch')?.addEventListener('click', switchAccount);
    show('confirm');
}

/**
 * Human label for the active session: the email captured during the OAuth
 * redirect when we have it, always alongside the wallet the invite will
 * actually bind to.
 */
function signedInIdentity(): string {
    const email = localStorage.getItem('email') ?? '';
    const claims = getTokenClaims();
    const wallet = typeof claims?.ethereum_address === 'string' ? claims.ethereum_address : '';
    const shortWallet = wallet.length > 12 ? `${wallet.slice(0, 6)}…${wallet.slice(-4)}` : wallet;
    if (email && shortWallet) return `${email} (${shortWallet})`;
    return email || shortWallet || 'an unknown account';
}

/** Drop the active session (keep the pending invite token) and offer login. */
function switchAccount() {
    localStorage.removeItem('token');
    localStorage.removeItem('email');
    show('login');
}

async function accept(token: string) {
    show('loading');
    try {
        const tenantId = await TenantService.getInstance().acceptInvitation(token);
        localStorage.removeItem(PENDING_KEY);
        // Record login so the new member's email + last-login populate immediately.
        try {
            await TenantService.getInstance().recordLogin(tenantId);
        } catch {
            // non-fatal
        }
        show('success');
        window.location.replace(`/#/${tenantId}/`);
    } catch (err) {
        localStorage.removeItem(PENDING_KEY);
        show('error', extractMessage(err) || 'We could not accept this invitation.');
    }
}

void run();
