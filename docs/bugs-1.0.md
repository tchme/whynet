# Bugs found while preparing 1.0

## Site URLs leaked through Markdown and plain-HTTP diagnostics

- **Symptom:** the default Markdown report included URL credentials, queries and fragments. When TLS failed or HTTPS redirected to HTTP, the diagnostics could send a sensitive address in an unencrypted request.
- **Cause:** the report masked only network and account identifiers. The Go probe reused the requested path and query for its HTTP fallback and followed downgrades. Chromium made its own redirect decisions.
- **Fix:** mask URL details in Markdown, including its embedded JSON. Keep the collected result intact. Request only the site's root for the HTTP stub fallback, clear its redirect referrers and stop downgrades. The hidden Chromium check stops HTTP requests in the inspected page through request-stage DevTools interception. A visible check requires fresh confirmation that Chromium may follow an unencrypted redirect.
- **Verified:** `TestMarkdownHidesSiteURLDetails`, `TestHTTPKeepsPageAddressOnTLS`, HAR and browser aggregation tests, and a local isolated Edge 158 run. The TLS server saw the original URL and a second HTTPS hop. The HTTP listener saw zero requests from the protected browser. A second HTTPS-only redirect loaded as expected. A control launched headlessly with the plain NetLog flags and no DevTools sent the HTTP destination's path and query. The built frontend with a stubbed Wails bridge asked again on every launch. Cancel and Escape did not call the bridge, and confirmation called it once with the original URL. An actual visible-window run on a separate test system remains pending.

## Incomplete hosts checks offered to change live entries

- **Symptom:** cancelling or timing out while checking a host could put its active line in the fixes.
- **Cause:** the TCP worker reported all errors as a dead address. TLS workers counted interrupted handshakes as TLS failures. Unchecked pairs counted only against the cap.
- **Fix:** record timeouts, cancellations, missing worker results and pairs skipped at the cap as unchecked. A line can enter the fix only if each of its names has a confirmed stale result. Count each unchecked address/name pair once.
- **Verified:** `TestHostsFixRequiresConfirmedPinFailures` compares the exact fix lines and unchecked counts through the real checker and plan, using temporary files, local TLS and injected network outcomes.

## Unverified Windows checks looked healthy or fixed

- **Symptom:** an unreadable area could show a green check. A missing finding after a failed operation or re-check could count as repaired. The second batch was compared with the first check.
- **Cause:** live events and the window replaced the probe status with a status based only on matched rules. The fix view counted disappearing rule IDs without checking the last batch's action or the evidence that was actually gathered.
- **Fix:** use one area status in progress, the window, the report and the CLI. Keep incomplete checks visible alongside known findings. Compare each batch with the check immediately preceding it. Credit a finding only when its planned operation succeeded and the re-check confirms its effect. For hosts, confirm the exact lines are inactive even if unrelated pairs could not be checked.
- **Verified:** app, report, CLI and rule tests cover missing facts, missing areas, failed actions, a second batch, partial hosts coverage, and positive controls. The fixtures do not change Windows settings or open the desktop app.

## Updates could install an unverified file or leave an unusable executable

- **Symptom:** a download with a missing digest reached the replacement step. A failure to start the replacement left the portable executable at its original path unusable.
- **Cause:** the digest check was optional, installation and restart were separate operations, and each process used the same file names without a lock.
- **Fix:** require a valid SHA-256 digest for installation and lock the target path through verification, replacement and launch. Use `ReplaceFileW` with a backup in the same directory, restore the backup on a launch failure, and keep a successful backup until the next replacement.
- **Verified on Windows 11 build 26200:** update tests cover missing, malformed and mismatched digests, an invalid PE file, a competing installer, and a forced process exit immediately before or after replacement of a running test executable. The surviving file launches from the original path in both interruption cases. A local test also restored the original bytes while the original image was still running. Windows 10 and an interrupted operation inside the Windows API remain unverified.

## Reports and HAR records could be associated with the wrong result

- **Symptom:** a site tab could offer a Windows network report. A HAR from a second page could be attached to the first page's check on the same domain.
- **Cause:** exports used the last completed mode, and HAR comparison used the registered domain as the page identity.
- **Fix:** use the requested tab for preview, save and copy. Compare HARs on the normalized HTTPS host and escaped path, with query strings removed. Check the HAR page first when the saved result is from another page, and publish the comparison together with that check. Use one helper for registered-domain comparisons in the HTTP probe.
- **Verification:** app and probe fixtures exercise missing tabs, several result types, page paths and ports, a failed re-check and an input query that stays out of the HAR re-check.

## Partial measurements could produce a confident all-clear

- **Symptom:** a direct HTTPS response could yield an all-clear with the browser check unavailable. The stability check could report a stable chain even when a measured address never responded.
- **Fix:** explain which part of the site check succeeded when its browser step is unavailable. Report partial stability when control or target responses are missing. A silent intermediate node alone never establishes loss of forwarded traffic. Describe the name-based failure as evidence about TCP and the observed route.
- **Verification:** rules fixtures check each partial site state, silent links and a frame stall with silent internet controls. The provider-side details of the original observations remain unconfirmed.

## Interrupted fixes could lose the information needed for undo

- **Symptom:** an interrupted batch could change settings before any undo appeared. An unreadable journal looked empty. Some failed or partial reversals could lose their history.
- **Cause:** the window saved undo after the entire batch, the helper received a batch of commands to run, and the journal was overwritten in place. Neither the journal nor the hosts write had a durable preparation step.
- **Fix:** serialize batches through the user journal lock. Save each reversible operation and its before and expected states before allowing it to run. Check incomplete operations, active writers and outside changes before undo. Preserve unreadable journal bytes and retain legacy entries when their ownership cannot be established. For hosts, verify the original copy and full replacement and use Windows replacement semantics.
- **Verification:** isolated Windows 11 tests exercised denied journal replacement, corruption, writer contention, lost helper responses, refused elevation, the version 0.7.0 format, service start followed by resync failure, partial DNS retry, and ordered batches. Temporary hosts tests verified Windows replacement failure and recovery, byte-exact copies, mixed line endings, a BOM and an intervening edit. The actual UAC prompt and live Windows settings remain release acceptance scenarios.
