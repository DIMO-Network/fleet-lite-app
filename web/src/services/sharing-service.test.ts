import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const get = vi.fn();
vi.mock('./api-service.ts', () => ({ ApiService: { getInstance: () => ({ get }) } }));

const { JobTimeoutError, JobWaitAbortedError, SharingService } = await import('./sharing-service.ts');

const pending = { jobId: 1, state: 'running', isSuccessful: false, errors: [] };
const done = { jobId: 1, state: 'completed', isSuccessful: true, errors: [] };

describe('SharingService.waitForShare', () => {
    beforeEach(() => vi.useFakeTimers());
    afterEach(() => {
        vi.useRealTimers();
        get.mockReset();
    });

    // Runs the poller to completion under fake timers and reports how it ended.
    async function settle(p: Promise<void>): Promise<unknown> {
        const outcome = p.then(() => 'ok', (e: unknown) => e);
        await vi.runAllTimersAsync();
        return outcome;
    }

    it('resolves once the job completes', async () => {
        get.mockResolvedValueOnce(pending).mockResolvedValueOnce(done);
        expect(await settle(SharingService.getInstance().waitForShare(7, 1))).toBe('ok');
    });

    it('rides out a dropped status read instead of reporting a failure', async () => {
        get.mockRejectedValueOnce(new Error('network')).mockResolvedValueOnce(pending).mockResolvedValueOnce(done);
        expect(await settle(SharingService.getInstance().waitForShare(7, 1))).toBe('ok');
    });

    it('reports unknown, not failed, when status reads keep failing', async () => {
        get.mockRejectedValue(new Error('network'));
        expect(await settle(SharingService.getInstance().waitForShare(7, 1))).toBeInstanceOf(JobTimeoutError);
    });

    it("surfaces the job's own error when it is discarded", async () => {
        get.mockResolvedValue({ jobId: 1, state: 'discarded', isSuccessful: false, errors: ['nothing was changed'] });
        const outcome = await settle(SharingService.getInstance().waitForShare(7, 1));
        expect(outcome).toBeInstanceOf(Error);
        expect(outcome).not.toBeInstanceOf(JobTimeoutError);
        expect((outcome as Error).message).toBe('nothing was changed');
    });

    it('stops polling when its caller goes away', async () => {
        get.mockResolvedValue(pending);
        const abort = new AbortController();
        const outcome = SharingService.getInstance().waitForShare(7, 1, abort.signal).catch((e: unknown) => e);
        await vi.advanceTimersByTimeAsync(4000);
        abort.abort();
        expect(await outcome).toBeInstanceOf(JobWaitAbortedError);
        const calls = get.mock.calls.length;
        await vi.advanceTimersByTimeAsync(60_000);
        expect(get.mock.calls.length).toBe(calls);
    });
});
