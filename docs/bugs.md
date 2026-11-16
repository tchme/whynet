# Bugs and pitfalls

Verified pitfalls and fixed bugs: symptom -> cause -> fix -> how it was verified.

## A VPN on the router looked like DNS spoofing

- **Symptom:** youtube.com: DNS step "warn" (`dns.match=differ`), TLS step took 6 s.
- **Cause:** the router runs a proxy in fake-ip mode (Clash/Mihomo/sing-box style): the system DNS answers with stand-in addresses from 198.18.0.0/15 and the router carries the traffic. The real address from DoH is not reachable directly, so the "alt" handshake waited out its timeout.
- **Fix:** answers entirely in 198.18.0.0/15 give `dns.match=fakeip`; no alt handshake then. New diagnosis `proxy_path_broken` for when that path fails, so a broken router VPN is not blamed on the ISP.
- **Verified:** live run on the author's network (2026-11-15): DNS ok, TLS 96 ms, `all_ok`; lab case "fake-ip proxy path broken".

## The exe showed no version, product or author in its properties

- **Symptom:** after setting `info` in wails.json, the exe's details tab (and `FileVersionInfo`) stayed empty, though the strings were inside the file.
- **Cause:** the Wails template `build/windows/info.json` writes the string table under language `0000`, which Windows doesn't read.
- **Fix:** the table is under `0409`, with FileVersion, InternalName and OriginalFilename added. The icon comes from `build/appicon.png`: delete `build/windows/icon.ico` after changing it, and Wails cuts a new one (256–16 px).
- **Verified:** `FileVersionInfo`: whynet 0.1.1, tchme, MIT License; the exe's own icon is the new one.

## ozon.ru: "turn off your VPN" was about automation, not the VPN

- **Symptom:** whynet said "the site doesn't let you in through a VPN" for ozon.ru, while the author's own Edge and Opera opened it fine through the same router VPN.
- **Cause:** ozon.ru turns away any browser with `navigator.webdriver` set, and Chromium sets it as soon as `--remote-debugging-port` is open; its block page just says "Похоже, нет соединения… выключите VPN". Bisected with a fresh-profile Edge: no flags — ozon opens; debugging port, even with no client attached — blocked; `--enable-automation` — blocked; `--log-net-log` — opens.
- **Fix:** the visible window is a plain browser recorded through Chromium's network log (`netlog.go`), no debugging port; `--test-type` hides the "unsupported command-line flag" bar the log flag brings up. A block page seen by the automated browser now means "the site turns away automated checks" (`bot_check`); only one seen by the plain window means the connection (`site_blocks_you`); a plain window that loaded the page gives `browser_ok`.
- **Verified:** visible ozon.ru: real storefront, 903 requests recorded live, video servers with speed; Edge and profile cleaned up. The final `browser_ok` verdict on screen was not captured (a table test covers it).

## The content check called working servers dead (redirect loop)

- **Symptom:** after a plain-window ozon.ru check: "www.ozon.ru, p.ozon.ru don't answer", though the browser loaded both.
- **Cause:** the sample fetch followed redirects without the site's cookies, hit the redirect limit and reported it as a network failure.
- **Fix:** any HTTP answer, a redirect included, counts as a working server (no redirects followed); a server the real browser loaded from is never counted as failed, only marked.
- **Verified:** the same check: both servers "работает".

## Pitfall for whoever tests the window: synthetic clicks go to the foreground app

Windows does not always let a script bring a window to the front. Test keystrokes and clicks sent to whynet's coordinates then land in whatever window is on top (it happened with the author's video player). Don't drive the GUI while the person uses the computer; ask first.

## Edge signed the throwaway profile into the person's Microsoft account (privacy)

- **Symptom:** the visible window showed "Данные браузера теперь синхронизируются…" with the person's account e-mail; the profile was named after it.
- **Cause:** Edge's implicit sign-in: a fresh profile on Windows signs into the Windows Microsoft account and starts sync on its own, headless included. Checked pages could reach the account's history; it also cost seconds of start-up (ozon.ru's page showed after 10 s, then 0.4 s).
- **Fix:** every browser run starts with `--disable-sync --disable-features=msImplicitSignin`.
- **Verified:** a headless Edge with a fresh profile: `account_info` with an e-mail in `Default\Preferences` without the flags, none with them; the visible window then shows "Профиль 1" and no sync prompt. Runs before 2026-11-16 (commit before this fix) did sign in.

## The visible window's verdict was lost when the person closed it

- **Symptom:** ozon.ru in a visible window gave `site_blocks_vpn` when ended with "Готово", but `bot_check` when the window was closed.
- **Cause:** the page title and text were read once at the end; a closed window can't be asked.
- **Fix:** a visible window is read every 2 s; the last reading is kept.
- **Verified:** close the Edge window during an ozon.ru check: `site_blocks_vpn`, browser and profile cleaned up.

## ozon.ru: "all fine, code 307" and no content checked

- **Symptom:** ozon.ru gave `all_ok` with "the site answered (code 307)"; the content step checked 0 servers.
- **Cause:** ozon sets a cookie and redirects; the HTTP client kept no cookies, looped 5 redirects and stopped on a 307 with no page in it. With cookies it reaches an anti-bot page (`Antibot Challenge Page`, 403), which a real browser passes by script.
- **Fix:** a cookie jar in the HTTP step; anti-bot pages recognised by title (`challenge` in markers.yaml) or Cloudflare's `Cf-Mitigated: challenge`, giving the `bot_check` verdict; the browser step reads the final page title and notes when it was stopped by the check too (ozon shows automated browsers "Похоже, нет соединения").
- **Verified:** live ozon.ru: `bot_check`, browser step note about the check.

## A slowly generated page counted as a slow network

- **Symptom:** yandex.ru got "content loads very slowly: dzen.ru ~58 KB/s" (another run: 184 KB/s).
- **Cause:** speed was computed for any big response, including the dzen.ru HTML page the server builds on the fly; its "speed" is the server's thinking time. The same for a server's root page fetched by the content check.
- **Fix:** speed only for files sent as they are: images, media, fonts, scripts, styles in the browser; in the content check only for a real sample file from the HTML, not a root page or an embedded page.
- **Verified:** yandex.ru `all_ok`; `TestBrowserAggregation` has a slow document that must not get a speed.

## The window grew under the taskbar

- **Symptom:** after opening "Details" the window looked unchanged, with a scrollbar.
- **Cause:** the small start window is centred on screen; growing it kept its top edge and pushed the bottom off the screen (y 621 + 1252 > 1392).
- **Fix:** when the new height doesn't fit below, move the window up first (`WindowGetPosition` / `WindowSetPosition`), at most to the top of the work area; height capped at 90% of the screen.
- **Verified:** screenshots of the built exe (100% scaling only), window rectangle read with `GetWindowRect`.

## Edge refused the DevTools connection ("bad status")

- **Symptom:** browser step failed at once: `websocket.Dial ws://127.0.0.1:…/devtools/browser/…: bad status`.
- **Cause:** Chromium 111+ rejects a DevTools client whose `Origin` is not allowed by `--remote-allow-origins`, and compares it as an exact string: `http://127.0.0.1/` (with the slash x/net/websocket was given) is not `http://127.0.0.1`.
- **Fix:** launch with `--remote-allow-origins=http://127.0.0.1` and dial with exactly that origin.
- **Verified:** youtube.com check: 164 requests recorded.

## A tag without the attribute added the page itself as a content server

- **Symptom:** `pageResources` listed the site's own host as image and media.
- **Cause:** `<img srcset=…>` without `src`, or `<video poster=…>` without `src`, passed "" to `base.Parse`, which resolves to the page URL.
- **Fix:** skip empty values. **Verified:** `TestPageResources`.

## Browser step: false "failed" from requests that hang on purpose, and a 20 s wait

- **Symptom:** habr.com sometimes got "part of the content didn't load" for habr.com itself; the browser step always used its whole 20 s.
- **Cause:** a hung request to a blocked ad server held back the page's load event; counting every unanswered request as a timeout also caught long-polling XHR/fetch/ping and single slow answers from a working server.
- **Fix:** stop 5 s after load, or 8 s after the page is parsed when load never comes; count silence as a timeout only for documents, scripts, styles, images, fonts and media; flag a server for silence only when none of its requests succeeded; ads excluded.
- **Verified:** habr.com three runs in a row `all_ok`, browser step ~10 s (was 20 s); `TestBrowserAggregation`.

## Report preview ran past the window and hid its buttons

- **Symptom:** in the AI report preview, Save / Copy / Close were below the bottom edge of the window.
- **Cause:** `max-height: 100%` on the dialog, a grid item centered in a fixed backdrop, does not resolve to the window height; the `<pre>` grew with the report.
- **Fix:** dialog `max-height: calc(100vh - 32px)`; the `<pre>` takes `flex: 1 1 360px; min-height: 0` and scrolls inside.
- **Verified:** screenshots of the built exe at 900x660 with a long report: buttons visible, preview scrolls.

## Windows: a refused connection on loopback takes ~2 s

- **Symptom:** a test dialing a closed local port classifies it as `timeout` under a short test timeout.
- **Cause:** Windows retries the SYN after a RST before reporting WSAECONNREFUSED (10061).
- **Fix:** the lab scripts "refused" as an `OpError` with errno 10061 instead of dialing a closed port. Real checks use a 6 s timeout, which covers the delay.
- **Verified:** `go test ./rules` case "refused".

## YAML: ": " inside a plain text value breaks the file

- **Symptom:** `whynet: yaml: line 50: mapping values are not allowed in this context` at startup.
- **Cause:** an unquoted value in `locales/ru.yaml` contained a colon followed by a space.
- **Fix:** rephrased with a dash. Quote any value that needs ": ". `TestRulesAndTextsAgree` loads both files, so a broken file fails the tests.
- **Verified:** CLI run and `go test ./...`.

## `frontend/dist` must exist for `go test ./...`

- **Symptom (avoided):** `pattern all:frontend/dist: no matching files found` on a fresh clone.
- **Cause:** `main.go` embeds `frontend/dist`, which the Wails scaffold's `.gitignore` excluded entirely.
- **Fix:** `.gitignore` keeps `frontend/dist/gitkeep`.

## "All fine" while the browser's page load was reset (linkedin.com, zapret)

- **Symptom:** a friend's run (Flex Ltd., zapret running): linkedin.com `all_ok` (high), though the browser step failed with `net::ERR_CONNECTION_RESET` on the page itself.
- **Cause:** whynet's own request got through (a reCAPTCHA page, HTTP 200), and no rule looked at `br.status=nav_failed`, so the rules fell through to `all_ok`. The same report without zapret had TLS ok and the request reset: `unknown`, as no rule knew a cut after the handshake either.
- **Fix:** `cut_midway` (TLS ok, then `http.status=reset` or the browser's `ERR_CONNECTION_RESET`), with the `dpi_*` twins when a bypass runs; any other `nav_failed` gives `unknown`, never `all_ok`.
- **Verified:** table cases built from both reports' facts. Not re-run on that network.

## zapret: "site not in the lists" for sites listed by address

- **Symptom:** rutracker.org with Flowseal zapret: "сайт в списках запрета: нет", though the bypass did act on it (handshake passed).
- **Cause:** only the name lists were read. Flowseal's `ipset-all.txt` holds whole Cloudflare and Google ranges (172.67.128.0/17, 104.16.0.0/12, 162.159.128.0/19, 142.250.0.0/15), and an empty `ipset-all.txt` is its "any" mode (`service.bat`: zero lines = any): every address gets the bypass.
- **Fix:** the TCP step checks the address against `ipset-*.txt` (`tcp.dpi_ipset`; an exclude list wins); `dpi_not_listed` needs both name and address unlisted.
- **Verified:** unit test `TestIPListed`; live on the author's PC (empty `ipset-all.txt`): `tcp.dpi_ipset=true`, entry `any`.

## A dead link counted as "the browser failed, the servers are fine"

- **Symptom:** a youtube.com video with zapret: `content_flaky`, "the browser failed on rr5---sn-t0a7lnee.googlevideo.com, though the separate check passed"; that check had actually said "no such domain".
- **Cause:** `gone` (neither the system DNS nor DoH knows the name) is not a failure in the content step, so the rule for browser-only failures fired. The player used other video nodes; nothing was broken.
- **Fix:** `res.browser_dead` when every server the browser failed on is `gone`; it gives `all_ok` before `content_flaky`.
- **Verified:** `TestContentLab` dead link case (`content_flaky` without the fix).

## A wrong clock could never be diagnosed

- **Symptom:** found while preparing the live test: `clock_wrong` had never fired; with the clock far off it would have said "bad certificate".
- **Cause:** the real time comes from HTTPS answers (`1.1.1.1/cdn-cgi/trace`, then `8.8.8.8`) with normal certificate checks. A clock wrong enough to break certificates breaks those requests too, so `local.clock` stayed `unknown`; a clock off by less breaks no certificate, and the rule needs a certificate error.
- **Fix:** the `8.8.8.8` fallback checks the certificate chain and name at the middle of the certificate's own validity instead of "now" (`verifyAnyTime`): still authenticated, independent of this PC's clock.
- **Verified:** live: clock 400 days back (only back: WireGuard clients on the PC reject a clock that jumped forward and back), `local.clock=wrong`, skew -34559810 s, `clock_wrong` (high); the clock restored by the same exact shift. `TestVerifyAnyTime`.

## A dead system proxy gave "unknown" (regression of the `nav_failed` rule)

- **Symptom:** with the Windows proxy on and pointing nowhere, the expected `proxy_in_way` would have been `unknown`: the browser fails with `ERR_PROXY_CONNECTION_FAILED`, and the new "browser couldn't open the page" rule came first.
- **Fix:** `proxy_in_way` (and `https_intercepted`, which outranked it) now come before the browser rules.
- **Verified:** live: `ProxyEnable=1` for one run (the PC's stale `127.0.0.1:12334`, nothing listening), `proxy_in_way`; the setting restored. Table case.

## The AI report showed "Г—1" and "вЂ”" (mojibake in the source)

- **Symptom:** in the friend's reports the browser table had `net::ERR_CONNECTION_RESET Г—1` and the content table `вЂ”` for "no ping".
- **Cause:** commit 4fa280b (0.1.1) saved two lines of `report.go` through a cp1251 round trip: `×` became `Г—`, `—` became `вЂ”`. Nothing tested those two strings.
- **Fix:** the characters restored; `TestMarkdownSymbols` checks both tables and looks for mojibake. When editing files from PowerShell 5.1, write UTF-8 explicitly (`Set-Content` defaults to the ANSI code page).
- **Verified:** `TestMarkdownSymbols`.

## The ISP name was missing for prefixes announced by several networks

- **Symptom:** found while building the trace's "whose network" column: 77.88.8.8 gave an AS of "13238 208398" and no name.
- **Cause:** Team Cymru's origin record lists every AS that announces a prefix, space-separated, in its first field; the code took the whole field as one number and asked for the name of "AS13238 208398". The site check's "Провайдер" line had the same bug for such ISPs.
- **Fix:** `originASN` takes the first number; `probe.ASN` is shared by the site check and the trace.
- **Verified:** `TestOriginASN`; live: 77.88.8.8 → AS13238 YANDEX, 89.179.0.1 → AS8402 CORBINA (Beeline).

## Found by driving the built app (stability check)

Checked in the running exe on the author's PC (2026-11-16: Start, Ctrl+Alt+L pressed by a script, Done, details, hover, compare). Three things the stubbed-window screenshots had not shown:

- **The lanes chart was solid "repeated spike" red.** `ChartSeries.State` was `[]uint8`; `encoding/json` writes a byte slice as a base64 string, so the window read characters of a string instead of 0/1/2. Now `[]int`; `TestChartJSON` checks the JSON is a list.
- **The window did not grow while recording.** The hint under the chart gets longer once the verdict is available, but the window was fitted only on a full render, and the recording screen is patched in place. The "Готово" button ended up under the bottom edge. `updateLiveRec` now calls `fitWindow`.
- **Spinners and the progress bar jerked every second.** The links list and the bar were rebuilt as HTML each second, which restarts CSS animations. The numbers are now patched in place (`patchLiveChain`), the bar only changes its width.
- Also: two new ru.yaml values had ": " inside a plain value (see the YAML pitfall above) and broke every text test and the build with `mapping values are not allowed`; quoted.

## "The lag was not the network" could blame the PC for a network spike (removed before release use)

- **Symptom:** none seen live; found in review with the author (2026-11-16, right after 0.2.0). A person presses Ctrl+Alt+L a second or three after the lag. The mark was read at ±2 s, Wi-Fi spikes last 1-2 s, so a late mark usually fell on a calm network; on a session whose one or two spikes stayed under the "unstable" bar, `live_not_network` then said the lag was the computer's.
- **Fix:** marks are bookmarks. Each looks back 10 s (and 1 s ahead) for the nearest confirmed jump and names it, or says there was none; the window shades those 10 s and the hover shows the mark's line. `live_not_network` and the `live.lag_calm` facts are gone: no verdict is drawn from marks. Measuring the PC (CPU, later frame times) is planned instead.
- **Verified:** `TestAnalyzeFriendWiFi` (a mark 2 s after a spike finds it at 2 s; a mark with no jump in the 10 s before says so); `TestLiveMatch` (marks alone give `live_stable`); window screenshots of the built frontend with the Go side stubbed.

## "All fine" while page blocks loaded late (dzen.ru, ru.pinterest.com)

- **Symptom:** the author's report for dzen.ru (2026-11-16): grey placeholder blocks in their browser, whynet `all_ok`; the only trace was privacy-cs.mail.ru with a 1020 ms ping (others 3-23 ms) that nothing reacted to. On ru.pinterest.com whynet showed an 11 s ping to images and 4 failed requests of 240, still `all_ok`, without saying which files.
- **Cause:** three gaps. The ping was one TCP connect, so a lost packet (Windows resends a connect after 1 s) looked like a number, not a problem: privacy-cs.mail.ru lost 4 of 6 connects in a row while top-fwz1.mail.ru on the same network lost none. The browser kept per server only its slowest time, no file and no phase. A lone request that never answered, from a server whose other requests came, was skipped on purpose (the habr.com false alarm), which is exactly one stuck image in a feed. Also, behind the fake-ip router the router answers a connect in 0 ms, so pinterest's image servers had no ping at all.
- **Fix:** slow files with their phases (`br.late*`), ping as 5 tries with lost tries counted (`res.lossy*`), a TLS handshake as the ping behind fake-ip, verdicts `content_late_loss`, `content_late_server`, `content_late`, `content_lossy` (see diagnoses.md). Cancelled requests are not slow files.
- **Verified:** `TestSlowFiles`, `TestPingStats`, table cases, the lab's stuck-image case; real Edge against a local server (one image delayed 3.5 s: `wait 3500`; one never answered: `none`, 8 s+); live 2026-11-16: dzen.ru `content_lossy` (privacy-cs.mail.ru 4 of 5), habr.com, youtube.com, ru.pinterest.com `all_ok` with no slow files, pinterest's image servers now 82-86 ms through the router. A live run with a really slow file was not caught: none came late that day.

## Requests hung while the network was quiet were not counted as hung

- **Symptom:** found while testing slow files: an image that never answered, watched for 8 s, was reported as 3.5 s and not as hung.
- **Cause:** "now" for open requests was the timestamp of the latest DevTools event; when the network went quiet, that clock stopped, so a request stuck after the last event never reached the 5 s "no answer" mark.
- **Fix:** at the end of the hidden run the event clock is moved on by the time since the last event.
- **Verified:** the same real-Edge run: the stuck image 8.1 s, `net::ERR_TIMED_OUT (no answer while whynet watched)`.

## A filtered browser record gave "everything loaded" (dzen.ru, the author's Opera)

- **Symptom:** the author's first full record of dzen.ru, with the grey blocks on screen, gave `own_clean`: "все 2 запросов из записи прошли".
- **Cause:** the record held 2 requests, dzen's page and its sign-in hop through sso.passport.yandex.ru, both documents: the Network tab was most likely filtered to "Doc" (or opened after the load). Two requests say nothing about a page that loads dozens; `own_clean` judged them anyway.
- **Fix:** a record under 5 requests is not judged (`ErrFewRequests`): the dialog asks to pick the "All" filter, reload and copy again; the instructions name the filter. Counts in the texts no longer need a Russian plural ("2 запросов").
- **Verified:** `TestParseHAR` (that record's two documents give `ErrFewRequests`); live the same day: the author's next record (filter All, 17 requests) gave `own_blocked`: their Opera's ad blocker stopped 4 of dzen's own app bundles on static.dzeninfra.ru (`net::ERR_BLOCKED_BY_CLIENT`, kept in the sanitized copy), the cause of the grey blocks.

## linkedin.com: "the server doesn't answer" while it answered (Google's balancer)

- **Symptom:** two friends' runs, 2026-11-16 (Beeline and ER-Telecom): linkedin.com `ip_unreachable`, "Сервер по адресу 130.211.32.14 не отвечает", though the TCP connect passed in 31-66 ms.
- **Cause:** after the real name was cut, the control handshake with `example.org` ended in a clean close (EOF), and anything but success or a TLS alert counted as `tls.control=failed`, "cut with any name". The server itself closes names it has no certificate for: the two ISPs cut the real name differently (timeout on Beeline, reset on ER-Telecom) and both saw the same EOF for the neutral one.
- **Fix:** EOF on the control is `tls.control=closed`; real name reset/timeout with `closed` gives `sni_suspected` (likely). A real name that also ends in EOF stays `unknown`: a server closing every name is not DPI.
- **Verified:** lab case (a server that resets one name and closes the rest), table cases, both reports replayed through the rules. Not re-run live: from the author's PC (Beeline) the neutral name to 130.211.32.14 also fails, which can't tell the server from the ISP.

## ER-Telecom's block page was not recognised

- **Symptom:** a friend's run on ER-Telecom (Dom.ru): linkedin.com and instagram.com redirect plain HTTP to `http://lawfilter.ertelecom.ru` with the page "Доступ закрыт"; whynet said `ip_unreachable` and `sni_blocked`.
- **Fix:** `lawfilter.ertelecom.ru` in the `stubs` markers (matched in the final URL). The page's text "Доступ закрыт" is not a marker: too common on ordinary 403 pages, and `isp_stub` is "high".
- **Verified:** the reports replayed: both give `isp_stub`.

## HTTP 429 gave "unknown"

- **Symptom:** instagram.com through a friend's router VPN: "Не удалось определить причину"; his browser showed `HTTP ERROR 429` too.
- **Fix:** `rate_limited` (likely) on `http.status=429`: the site limits the address, often a VPN's shared one.
- **Verified:** lab case (a server answering 429), table case, the report replayed.

## The browser step was skipped without saying why (no Edge, no Chrome)

- **Symptom:** a friend's Windows 10: `br.status=unavailable` in every report, no reason in it.
- **Cause:** only Edge and Chrome were looked for; that PC has neither (Edge removed, most likely).
- **Fix:** Brave and Vivaldi are looked for too, App Paths values are unquoted (Opera's installer writes the path in quotes), and the evidence says what was looked for. Tried live on the author's PC and left out: Yandex Browser refuses remote debugging ("DevTools remote debugging is disallowed by the system admin"), Opera GX opens the port but its navigation ends in `ERR_ABORTED`.
- **Verified:** Yandex Browser and Opera GX were run through `BrowserSession` here (results above; no processes or profile folders left). Brave and Vivaldi were not run: not installed on the author's PC.

## App check: a program dropping a spare connection looked like a block (Telegram)

- **Symptom:** found in the first live runs of the app check (2026-11-16): Telegram, working fine, got `app_blocked_maybe` ("похоже, блокируют") for 149.154.167.41:443.
- **Cause:** one of its 6 connections to that server vanished within 2 s without a closing state, and any such connection made the server "troubled". Programs abort spare parallel tries themselves (Telegram races its data centres; a closesocket with linger 0 is a reset too), so a lone reset among working connections is not a block.
- **Fix:** a server is troubled only when its failed connects (reset or unanswered) are at least as many as its working ones.
- **Verified:** `TestTroubled`; live: Telegram, Spotify, Yandex Messenger and Discord `app_ok` on the author's PC.

## App check: a console program named on the command line was "not running"

- **Symptom:** `whynet-cli -app blackhole-test` said the program is not running while it was.
- **Cause:** the picker counts a windowless process under its windowed ancestor (Steam's helpers under Steam); the test program, started from a terminal inside VS Code, counted under Code. The window's list also hides programs with no window and no connections at that moment.
- **Fix:** `FindProgram` (and the recording) find a named exe or PID among all processes; a named exe counted under another program becomes a program of its own.
- **Verified:** `TestFindProgram`; live: the test program found and recorded (`app_unreachable`).

## YouTube "loads part of its content late" for nearly everyone (whynet's hidden browser)

- **Symptom:** friends' reports through a VPN and through zapret (2026-11-16/17): youtube.com `content_late` or `content_flaky`, late files `fonts.gstatic.com/s/roboto/...woff2` (1 of 84 requests to that server), `www.gstatic.com/inputtools/images/tia.png` (the search box's keyboard icon), `www.gstatic.com/cv/js/sender/v1/cast_sender.js`, all "no answer while whynet watched".
- **Cause:** not found. Reproduced on the author's PC in 3 of 4 runs and in 2 of 5 with `--disable-quic` (so not QUIC); DevTools shows the request was sent (`requestWillBeSentExtraInfo` came); curl gets the same files in 0.2 s. Whatever holds them, nothing a person sees is missing: the text font falls back, the other two are an icon and the cast-to-TV script.
- **Fix:** `late_noise` in `markers.yaml` (addresses matched in the file URL) are not late files. A shortcut by address, marked so in the file.
- **Verified:** content lab case (the icon stuck on a server whose other 14 files came: `all_ok`; `content_late` with the entry removed); a friend's three YouTube reports replayed: `all_ok` (one kept its dead googlevideo link, which `res.browser_dead` already forgives).
