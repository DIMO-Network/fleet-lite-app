import { msg, str } from '@lit/localize';
import { VehicleCard } from '../types/vehicle.ts';

/** Everything the reason depends on, so a caller can't forget half of it. */
type ShareTarget = Pick<VehicleCard, 'canShare' | 'shareBlocker' | 'owner'>;

/**
 * A wallet as `0x1234…cdef`. Full addresses are unreadable inline and every
 * surface that shows one puts the whole value in a title attribute instead.
 */
export function shortWallet(address: string): string {
    return address.length > 12 ? `${address.slice(0, 6)}…${address.slice(-4)}` : address;
}

/**
 * Why this vehicle can't be shared, or null when it can.
 *
 * Lives here rather than on the list view because two surfaces say it now — the
 * row tooltip and the share modal's banner — and a second copy of these
 * sentences would drift the moment one of them was reworded.
 *
 * The order matters: a member without manage_vehicles hears that first. A
 * per-vehicle reason would send them to chase an owner over a permission they
 * couldn't act on afterwards anyway.
 */
export function shareBlockReason(
    v: ShareTarget,
    memberCanShare: boolean,
    myWallet: string,
): string | null {
    if (!memberCanShare) {
        return msg('Sharing needs the manage-vehicles permission');
    }
    if (v.canShare) return null;
    switch (v.shareBlocker) {
        case 'owner': {
            const short = v.owner ? shortWallet(v.owner) : msg('its owner');
            return myWallet && v.owner && v.owner.toLowerCase() === myWallet.toLowerCase()
                ? msg(str`Owned by you (${short}) — personally owned vehicles can't be fleet-shared`)
                : msg(str`Owned by ${short} — this account hasn't authorized fleet sharing`);
        }
        case 'no_owner':
            return msg('No owner on record — can\'t be shared');
        default:
            // 'unknown', and anything a newer API adds: the check didn't
            // complete, which is not a refusal and may resolve on the next
            // render. Nothing here may read as a decision about the owner.
            return msg('Sharing status couldn\'t be checked — try reloading');
    }
}
