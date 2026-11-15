# Bugs and pitfalls

Verified pitfalls and fixed bugs: symptom -> cause -> fix -> how it was verified.

## A VPN on the router looked like DNS spoofing

- **Symptom:** youtube.com: DNS step "warn" (`dns.match=differ`), TLS step took 6 s.
- **Cause:** the router runs a proxy in fake-ip mode (Clash/Mihomo/sing-box style): the system DNS answers with stand-in addresses from 198.18.0.0/15 and the router carries the traffic. The real address from DoH is not reachable directly, so the "alt" handshake waited out its timeout.
- **Fix:** answers entirely in 198.18.0.0/15 give `dns.match=fakeip`; no alt handshake then. New diagnosis `proxy_path_broken` for when that path fails, so a broken router VPN is not blamed on the ISP.
- **Verified:** live run on the author's network (2026-11-15): DNS ok, TLS 96 ms, `all_ok`; lab case "fake-ip proxy path broken".

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
