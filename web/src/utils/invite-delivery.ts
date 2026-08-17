import { msg, str } from '@lit/localize';
import { Invitation } from '../services/tenant-service.ts';

/**
 * Presentation for an invitation's email-delivery state. The status is stamped
 * when Postmark accepts the message and upgraded by the delivery/open/bounce
 * webhook that fleet-tenancy-api receives, so it only ever moves forward:
 * sent → delivered → opened, with bounced overriding all.
 *
 * An absent status means the email never dispatched — the send failed, or
 * sending is disabled (local dev). That is the case an owner most needs to see,
 * because the invite row exists and looks healthy while nothing was ever sent.
 *
 * Every helper takes the whole invitation: the label is only meaningful next to
 * the timestamp and bounce reason that came with it.
 *
 * Note: `msg()` is called inside these functions, never at module scope — a
 * module-level call would freeze the source locale (see docs/LOCALIZATION.md).
 */

/** Semantic tone driving the badge's colour. */
export type DeliveryTone = 'good' | 'bad' | 'warn' | 'neutral';

export function deliveryTone(i: Invitation): DeliveryTone {
    switch (i.emailStatus) {
        case 'opened':
        case 'delivered':
            return 'good';
        case 'bounced':
            return 'bad';
        case 'sent':
            return 'neutral';
        default:
            return 'warn'; // never dispatched
    }
}

export function deliveryIcon(i: Invitation): string {
    switch (i.emailStatus) {
        case 'opened':
            return 'drafts';
        case 'delivered':
            return 'mark_email_read';
        case 'bounced':
            return 'report';
        case 'sent':
            return 'send';
        default:
            return 'mail_off';
    }
}

/** Short badge text, e.g. "Opened". */
export function deliveryLabel(i: Invitation): string {
    switch (i.emailStatus) {
        case 'opened':
            return msg('Opened');
        case 'delivered':
            return msg('Delivered');
        case 'bounced':
            return msg('Bounced');
        case 'sent':
            return msg('Sent');
        default:
            return msg('Not sent');
    }
}

/**
 * Tooltip copy: the state in a full sentence, plus when it was reached and the
 * bounce reason when there is one.
 */
export function deliveryDetail(i: Invitation): string {
    const when = i.emailStatusAt ? new Date(i.emailStatusAt).toLocaleString() : '';
    const parts: string[] = [];
    switch (i.emailStatus) {
        case 'opened':
            parts.push(when ? msg(str`Email opened ${when}`) : msg('Email opened'));
            break;
        case 'delivered':
            parts.push(when ? msg(str`Email delivered ${when}`) : msg('Email delivered'));
            break;
        case 'bounced':
            parts.push(when ? msg(str`Email bounced ${when}`) : msg('Email bounced'));
            parts.push(msg('The address may be wrong or unreachable — fix it and invite again.'));
            break;
        case 'sent':
            parts.push(when ? msg(str`Email sent ${when}`) : msg('Email sent'));
            parts.push(msg('Not confirmed delivered yet.'));
            break;
        default:
            parts.push(msg('The invitation email was never sent. Use Resend to try again.'));
    }
    if (i.emailStatusDetail) parts.push(i.emailStatusDetail);
    return parts.join(' · ');
}
