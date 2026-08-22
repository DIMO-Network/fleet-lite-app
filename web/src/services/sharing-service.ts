import { ApiService } from './api-service.ts';

/** POST /vehicles/:tokenId/share and DELETE …/share/:grantee — the queued job. */
interface ShareResponse {
    jobId: number;
}

/**
 * Polling ran out of attempts. The outcome is UNKNOWN, not failed: the job is
 * not cancelled by our giving up on it and may still land afterwards.
 *
 * A separate type because the two callers have to tell this apart from a real
 * failure. A revoke especially — reporting "not revoked" for a job that is
 * still running would have somebody re-issue a grant they already withdrew.
 */
export class JobTimeoutError extends Error {}

/**
 * GET /vehicles/:tokenId/share/status.
 *
 * Success is `isSuccessful`, never a status string. The oracle carries both
 * conventions for different operations — per-VIN results use `status ===
 * "Success"` — and sharing is deliberately the boolean one. Reading the wrong
 * field here would report every share as failed.
 */
export interface ShareStatus {
    jobId: number;
    state: string;
    isSuccessful: boolean;
    errors: string[];
}

/** How long a share may take before the UI gives up waiting. */
const POLL_INTERVAL_MS = 4000;
const POLL_ATTEMPTS = 30;

/**
 * Singleton client over the tenant-scoped vehicle-sharing API.
 *
 * A share is an on-chain SACD grant made by fleet-tenancy-api's signer on the
 * vehicle owner's kernel account. It waits on a bundler, so the endpoint
 * returns a job id and this service polls.
 */
export class SharingService {
    private static instance: SharingService;

    public static getInstance(): SharingService {
        if (!SharingService.instance) {
            SharingService.instance = new SharingService();
        }
        return SharingService.instance;
    }

    /** POST /vehicles/:tokenId/share — queue the grant, returning its job id. */
    public async share(tokenId: number, grantee: string, durationDays: number): Promise<number> {
        const res = await ApiService.getInstance().post<ShareResponse>(
            `/vehicles/${tokenId}/share`,
            { grantee, durationDays },
        );
        return res.jobId;
    }

    /**
     * DELETE /vehicles/:tokenId/share/:grantee — queue the withdrawal,
     * returning its job id.
     *
     * What lands on chain is a *zeroed* SACD record — permissions 0, expiration
     * 0 — not a deleted one, so identity-api keeps returning the grantee
     * afterwards with an expiry at the epoch. Anything listing grants has to
     * drop expired ones or a successful revoke looks like it did nothing.
     */
    public async revoke(tokenId: number, grantee: string): Promise<number> {
        const res = await ApiService.getInstance().delete<ShareResponse>(
            `/vehicles/${tokenId}/share/${grantee}`,
        );
        return res.jobId;
    }

    /** GET /vehicles/:tokenId/share/status?jobId= */
    public status(tokenId: number, jobId: number): Promise<ShareStatus> {
        return ApiService.getInstance().get<ShareStatus>(
            `/vehicles/${tokenId}/share/status?jobId=${jobId}`,
        );
    }

    /**
     * Poll until the share lands, fails, or we stop waiting.
     *
     * Running out of attempts throws rather than resolving false, and says so:
     * the grant may still land afterwards, because the job is not cancelled by
     * our giving up on it. Telling the customer "it failed" would be wrong, and
     * silently resolving would be worse.
     */
    public waitForShare(tokenId: number, jobId: number): Promise<void> {
        return this.waitForJob(tokenId, jobId, {
            failed: 'The share could not be completed.',
            timeout:
                'The share is taking longer than expected. It may still complete — ' +
                'check the shared-with list in a moment.',
        });
    }

    /**
     * Poll until the revoke lands, fails, or we stop waiting.
     *
     * Same queue and the same status route as a share — there is deliberately
     * no revoke-status endpoint — so this is the same poller with the wording
     * the customer needs.
     */
    public waitForRevoke(tokenId: number, jobId: number): Promise<void> {
        return this.waitForJob(tokenId, jobId, {
            failed: 'The access could not be revoked.',
            timeout:
                'The revoke is taking longer than expected. It may still complete — ' +
                'check the shared-with list in a moment.',
        });
    }

    /**
     * The one poller. Shares and revokes are the same job table read through
     * the same status route, so they differ only in what to call the outcome.
     *
     * Note it sleeps first: the earliest read is t+POLL_INTERVAL_MS, because a
     * job queued a millisecond ago has never once been finished.
     */
    private async waitForJob(
        tokenId: number,
        jobId: number,
        messages: { failed: string; timeout: string },
    ): Promise<void> {
        for (let attempt = 0; attempt < POLL_ATTEMPTS; attempt++) {
            await new Promise((r) => setTimeout(r, POLL_INTERVAL_MS));

            const status = await this.status(tokenId, jobId);
            if (status.isSuccessful) return;

            // A terminal failure is worth surfacing immediately rather than
            // polling out the clock on a job that will never change.
            if (status.state === 'discarded' || status.state === 'cancelled') {
                throw new Error(status.errors?.[0] || messages.failed);
            }
        }
        throw new JobTimeoutError(messages.timeout);
    }
}
