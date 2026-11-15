# Bugs and pitfalls

Verified pitfalls and fixed bugs: symptom -> cause -> fix -> how it was verified.

## A VPN on the router looked like DNS spoofing

- **Symptom:** youtube.com: DNS step "warn" (`dns.match=differ`), TLS step took 6 s.
- **Cause:** the router runs a proxy in fake-ip mode (Clash/Mihomo/sing-box style): the system DNS answers with stand-in addresses from 198.18.0.0/15 and the router carries the traffic. The real address from DoH is not reachable directly, so the "alt" handshake waited out its timeout.
- **Fix:** answers entirely in 198.18.0.0/15 give `dns.match=fakeip`; no alt handshake then. New diagnosis `proxy_path_broken` for when that path fails, so a broken router VPN is not blamed on the ISP.
- **Verified:** live run on the author's network (2026-11-15): DNS ok, TLS 96 ms, `all_ok`; lab case "fake-ip proxy path broken".

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
