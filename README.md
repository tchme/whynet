<div align="center">

# whynet

**Почему не открывается сайт? whynet находит, где рвётся путь, и объясняет простыми словами.**

[![Релиз](https://img.shields.io/github/v/release/tchme/whynet?color=34c3a1&label=%D1%80%D0%B5%D0%BB%D0%B8%D0%B7)](../../releases/latest)
![Windows 10 | 11](https://img.shields.io/badge/Windows-10%20%7C%2011-34c3a1)
[![MIT](https://img.shields.io/badge/%D0%BB%D0%B8%D1%86%D0%B5%D0%BD%D0%B7%D0%B8%D1%8F-MIT-34c3a1)](LICENSE)

**Русский** · [English](#english)

![whynet](docs/screenshot.png)

</div>

Один небольшой `.exe`, без установки и прав администратора. Проходит путь от твоего компьютера до сайта: сеть, DNS, соединение, страница, её картинки, видео и скрипты. Находит шаг, на котором ломается, и говорит, что делать. Технические подробности — под кнопкой «Подробнее».

## Скачать

Возьми `whynet.exe` на странице [Releases](../../releases/latest) и запусти. Windows может показать предупреждение «неизвестный издатель»: exe не подписан. Edge или Chrome нужны только для проверки содержимого страницы (в Windows 10/11 Edge есть). Новые версии программа находит сама и предлагает обновиться.

## Что умеет

| | |
|---|---|
| **Проверка сайта** | Компьютер и сеть, DNS, подключение, TLS, ответ сайта. Находит блокировку по имени (DPI), подмену DNS, заглушки провайдера, прокси, плохой сертификат, неверные часы, не работающий обход блокировок. |
| **Содержимое страницы** | Страница открывается в невидимом Edge/Chrome, записывается каждый запрос. Для каждого сервера, с которого грузятся картинки, видео и скрипты: пинг (5 попыток, потери пакетов), скорость, замирание после первых КБ. Реклама и счётчики видны, но виноватыми не считаются. |
| **Медленные файлы** | Какой именно файл грузился долго или не загрузился, с какого адреса и на каком этапе ушло время: DNS, подключение, шифрование, ответ сервера или скачивание. |
| **Твой браузер** | Если у тебя блоки не догружаются, а у whynet всё в порядке, скопируй запись сети из своего браузера (F12 → Network → Copy all as HAR). whynet сравнит её со своей проверкой и покажет, что остановил сам браузер, например блокировщик рекламы. Cookies из записи не читаются. |
| **Окно браузера** | Обычное окно, которым пользуешься ты сам (пройти проверку на робота, войти, включить видео), пока whynet записывает, что грузит страница. |
| **Стабильность для игр** | Пингует роутер, провайдера, интернет и игровой сервер одновременно и показывает, какое звено даёт скачки пинга. Кнопка Ctrl+Alt+L отмечает момент, когда лагнуло. |
| **Отчёт для ИИ** | Одна кнопка сохраняет всё, что узнал whynet, в один Markdown-файл с инструкцией для нейросети наверху. Перетащи его в ChatGPT или Claude и ничего не пиши. |

Около 40 диагнозов, у каждого понятное объяснение и совет. Если уверенности нет, whynet так и говорит: «скорее всего» и почему.

## Отчёт для ИИ

Внешний IP, имя компьютера и имя пользователя по умолчанию скрыты. Перед сохранением видно, что попадёт в файл, и скрытие можно выключить.

## Приватность

whynet ничего не отправляет, кроме нужного для проверки: DNS-over-HTTPS-запросы про проверяемый домен (1.1.1.1, 8.8.8.8), запрос к Cloudflare, чтобы узнать внешний IP и провайдера, и сама страница. При запуске он спрашивает у GitHub номер последней версии и предлагает обновиться; обновляется, только если нажать «Да» (новый exe сверяется с контрольной суммой SHA-256 релиза). Телеметрии нет. Невидимый браузер работает во временном профиле без входа в аккаунт и синхронизации, профиль удаляется. Запись браузера (HAR) читается только на твоём компьютере: из неё берутся адреса без параметров, тип, статус, ошибка и время.

## Сборка из исходников

Нужны Go, Node.js и [Wails v2](https://wails.io).

```
go test ./...                              # тесты, включая лабораторию с имитацией поломок
go run ./cmd/whynet-cli -save youtube.com  # консольная версия, сохраняет отчёт для ИИ
wails build                                # build/bin/whynet.exe
```

Код разделён на пробы (собирают факты), правила (`rules/diagnoses.yaml` превращают факты в диагноз) и тексты (`locales/ru.yaml`). Устройство описано в [CLAUDE.md](CLAUDE.md) и [docs/](docs/) (на английском): замысел, архитектура, каталог диагнозов, известные подводные камни.

## Лицензии

MIT, см. [LICENSE](LICENSE). Собрано на [Wails](https://wails.io). Список рекламы и трекеров — [StevenBlack/hosts](https://github.com/StevenBlack/hosts) (MIT, `rules/ads-LICENSE.txt`). Шрифты Golos Text и JetBrains Mono (SIL OFL) входят через Fontsource.

---

<a id="english"></a>

<div align="center">

# whynet — English

**Why doesn't this site open? whynet finds where the path breaks and explains it in plain words.**

[Русский](#whynet) · **English**

</div>

One small `.exe`, no installation, no admin rights. It walks the path from your computer to the site: network, DNS, connection, the page and its images, video and scripts. It finds the step where things break and tells you what to do, with the technical details one click away under "Подробнее" (Details). The interface is in Russian.

## Download

Take `whynet.exe` from the [Releases](../../releases/latest) page and run it. Windows may show an "unknown publisher" warning: the exe is not code-signed. Edge or Chrome is needed only for the content checks (Edge ships with Windows 10/11). The app finds new versions itself and offers to update.

## What it does

| | |
|---|---|
| **Site check** | Computer and network, DNS, connection, TLS, the site's answer. Finds blocking by site name (DPI), DNS spoofing, ISP block pages, proxies, bad certificates, a wrong clock, a broken bypass tool. |
| **Page content** | The page opens in a hidden Edge/Chrome and every request is recorded. For each server the page loads images, video and scripts from: ping (5 tries, packet loss), speed, freezes after the first KB. Ads and trackers are listed but never blamed. |
| **Slow files** | Which exact file loaded slowly or not at all, from which address, and where the time went: DNS, connect, TLS, the server's answer or download. |
| **Your own browser** | When blocks hang for you but whynet finds nothing, copy a network record from your browser (F12 → Network → Copy all as HAR). whynet compares it with its own check and shows what your browser stopped itself, such as an ad blocker. Cookies in the record are never read. |
| **Browser window** | A plain window you use yourself (pass a bot check, log in, play a video) while whynet records what the page loads. |
| **Stability for games** | Pings the router, the ISP, the internet and a game server at once and shows which link makes the ping jump. Ctrl+Alt+L marks the moment it lagged. |
| **AI report** | One button saves everything whynet learned into a single Markdown file with an instruction for the AI at the top. Drop it into ChatGPT or Claude and ask nothing. |

About 40 diagnoses, each with a plain explanation and what to do. When whynet can't tell, it says "probably" and why.

## The AI report

Your public IP, computer name and Windows user name are hidden by default. You see exactly what goes into the file before saving, and can switch hiding off.

## Privacy

whynet sends nothing anywhere except what a check needs: DNS-over-HTTPS lookups of the checked domain (1.1.1.1, 8.8.8.8), a request to Cloudflare to learn your public IP and provider, and the page itself. On start it asks GitHub for the latest version number and offers to update; it updates only when you press "Yes" (the new exe is checked against the release's SHA-256). No telemetry. The hidden browser uses a throwaway profile with sign-in and sync switched off, deleted afterwards. A browser record (HAR) is read on your computer only: addresses without queries, type, status, error and timings are taken from it.

## Build from source

Needs Go, Node.js and [Wails v2](https://wails.io).

```
go test ./...                              # tests, including a lab of simulated failures
go run ./cmd/whynet-cli -save youtube.com  # console version, saves the AI report
wails build                                # build/bin/whynet.exe
```

The code is split into probes (collect facts), rules (`rules/diagnoses.yaml` turn facts into a diagnosis) and texts (`locales/ru.yaml`). See [CLAUDE.md](CLAUDE.md) and [docs/](docs/): design, architecture, the catalog of diagnoses, known pitfalls.

## Credits and licenses

MIT, see [LICENSE](LICENSE). Built with [Wails](https://wails.io). The ad and tracker list is [StevenBlack/hosts](https://github.com/StevenBlack/hosts) (MIT, `rules/ads-LICENSE.txt`). Fonts Golos Text and JetBrains Mono (SIL OFL) are bundled via Fontsource.
