<div align="center">

# whynet

**Почему не открывается сайт? whynet находит, где рвётся путь, и объясняет простыми словами.**

[![Релиз](https://img.shields.io/github/v/release/tchme/whynet?color=34c3a1&label=%D1%80%D0%B5%D0%BB%D0%B8%D0%B7)](../../releases/latest)
![Windows 10 | 11](https://img.shields.io/badge/Windows-10%20%7C%2011-34c3a1)
[![MIT](https://img.shields.io/badge/%D0%BB%D0%B8%D1%86%D0%B5%D0%BD%D0%B7%D0%B8%D1%8F-MIT-34c3a1)](LICENSE)

**Русский** · [English](#english)

![whynet](docs/screenshot.png)

</div>

Один небольшой `.exe`, без установки; права администратора нужны только для необязательной записи UDP. Проходит путь от твоего компьютера до сайта: сеть, DNS, соединение, страница, её картинки, видео и скрипты. Находит шаг, на котором ломается, и говорит, что делать. Технические подробности — под кнопкой «Подробнее».

## Скачать

Возьми `whynet.exe` на странице [Releases](../../releases/latest) и запусти. Windows может показать предупреждение «неизвестный издатель»: exe не подписан. Edge, Chrome, Brave или Vivaldi нужны только для проверки содержимого страницы (в Windows 10/11 Edge есть; Яндекс Браузер и Opera для неё не подходят). Новые версии программа находит сама и предлагает обновиться.

## Что умеет

| | |
|---|---|
| **Проверка сайта** | Компьютер и сеть, DNS, подключение, TLS, ответ сайта. Находит блокировку по имени (DPI), подмену DNS, заглушки провайдера (любого: по тексту закона о блокировках), прокси, лимит запросов, плохой сертификат, неверные часы, не работающий обход блокировок. |
| **Содержимое страницы** | Страница открывается в невидимом Edge/Chrome, записывается каждый запрос. Для каждого сервера, с которого грузятся картинки, видео и скрипты: пинг (5 попыток, потери пакетов), скорость, замирание после первых КБ. Реклама и счётчики видны, но виноватыми не считаются. |
| **Медленные файлы** | Какой именно файл грузился долго или не загрузился, с какого адреса и на каком этапе ушло время: DNS, подключение, шифрование, ответ сервера или скачивание. |
| **Твой браузер** | Если у тебя блоки не догружаются, а у whynet всё в порядке, скопируй запись сети из своего браузера (F12 → Network → Copy all as HAR). whynet сравнит её со своей проверкой и покажет, что остановил сам браузер, например блокировщик рекламы. Cookies из записи не читаются. |
| **Окно браузера** | Обычное окно, которым пользуешься ты сам (пройти проверку на робота, войти, включить видео), пока whynet записывает, что грузит страница. |
| **Стабильность для игр** | Пингует роутер, провайдера, интернет и игровой сервер одновременно и показывает, какое звено даёт скачки пинга. Кнопка Ctrl+Alt+L отмечает момент, когда лагнуло. Рядом записывается загрузка процессора, чтобы было видно, когда лаг от компьютера, а не от сети. Если на пинги отвечает VPN-клиент на этом компьютере, whynet это замечает и не выдаёт «всё стабильно». |
| **Проверка программы** | Выбираешь запущенную программу (Discord, лаунчер, игру), повторяешь в ней то, что не работает, а whynet записывает, к каким серверам она подключается по TCP, и проверяет каждый: отвечает ли, режут ли по имени, покрывает ли его zapret. TCP без прав администратора; с галочкой «И UDP» (Windows спросит права) whynet видит и UDP — голос Discord и саму игру: отвечает ли сервер, не замирает ли его поток. |
| **Отчёт для ИИ** | Одна кнопка сохраняет всё, что узнал whynet, в один Markdown-файл с инструкцией для нейросети наверху. Перетащи его в ChatGPT или Claude и ничего не пиши. |

Больше 70 диагнозов, у каждого понятное объяснение и совет. Если уверенности нет, whynet так и говорит: «скорее всего» и почему.

## Отчёт для ИИ

Внешний IP, имя компьютера и имя пользователя по умолчанию скрыты. Перед сохранением видно, что попадёт в файл, и скрытие можно выключить.

## Приватность

whynet ничего не отправляет, кроме нужного для проверки: DNS-over-HTTPS-запросы про проверяемый домен (1.1.1.1, 8.8.8.8), запрос к Cloudflare, чтобы узнать внешний IP и провайдера, и сама страница. При запуске он спрашивает у GitHub номер последней версии и предлагает обновиться; обновляется, только если нажать «Да» (новый exe сверяется с контрольной суммой SHA-256 релиза). Телеметрии нет. Невидимый браузер работает во временном профиле без входа в аккаунт и синхронизации, профиль удаляется. Запись браузера (HAR) читается только на твоём компьютере: из неё берутся адреса без параметров, тип, статус, ошибка и время.

## Сборка из исходников

Нужны Go, Node.js и [Wails v2](https://wails.io).

```
go test ./...                              # тесты, включая лабораторию с имитацией поломок
go run ./cmd/whynet-cli -save youtube.com  # консольная версия, сохраняет отчёт для ИИ
go run ./cmd/whynet-cli -app [-udp] -for 20s discord  # проверка программы из консоли (-udp: и UDP, права администратора)
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

One small `.exe`, no installation; admin rights are needed only for the optional UDP recording. It walks the path from your computer to the site: network, DNS, connection, the page and its images, video and scripts. It finds the step where things break and tells you what to do, with the technical details one click away under "Подробнее" (Details). The interface is in Russian.

## Download

Take `whynet.exe` from the [Releases](../../releases/latest) page and run it. Windows may show an "unknown publisher" warning: the exe is not code-signed. Edge, Chrome, Brave or Vivaldi is needed only for the content checks (Edge ships with Windows 10/11; Yandex Browser and Opera don't work for it). The app finds new versions itself and offers to update.

## What it does

| | |
|---|---|
| **Site check** | Computer and network, DNS, connection, TLS, the site's answer. Finds blocking by site name (DPI), DNS spoofing, ISP block pages (any ISP's, recognised by the blocking law's text), proxies, rate limits, bad certificates, a wrong clock, a broken bypass tool. |
| **Page content** | The page opens in a hidden Edge/Chrome and every request is recorded. For each server the page loads images, video and scripts from: ping (5 tries, packet loss), speed, freezes after the first KB. Ads and trackers are listed but never blamed. |
| **Slow files** | Which exact file loaded slowly or not at all, from which address, and where the time went: DNS, connect, TLS, the server's answer or download. |
| **Your own browser** | When blocks hang for you but whynet finds nothing, copy a network record from your browser (F12 → Network → Copy all as HAR). whynet compares it with its own check and shows what your browser stopped itself, such as an ad blocker. Cookies in the record are never read. |
| **Browser window** | A plain window you use yourself (pass a bot check, log in, play a video) while whynet records what the page loads. |
| **Stability for games** | Pings the router, the ISP, the internet and a game server at once and shows which link makes the ping jump. Ctrl+Alt+L marks the moment it lagged. The CPU load is recorded alongside, so a lag caused by the computer shows up next to one caused by the network. If a VPN client on this PC answers the pings itself, whynet notices and doesn't claim "all stable". |
| **App check** | Pick a running program (Discord, a launcher, a game), repeat in it what fails, and whynet records which servers it connects to over TCP and checks each one: does it answer, is it cut by name, does zapret cover it. TCP needs no admin rights; with the "И UDP" checkbox (Windows asks for them) whynet sees UDP too: Discord voice and the game itself, whether the server answers and whether its stream freezes. |
| **AI report** | One button saves everything whynet learned into a single Markdown file with an instruction for the AI at the top. Drop it into ChatGPT or Claude and ask nothing. |

More than 70 diagnoses, each with a plain explanation and what to do. When whynet can't tell, it says "probably" and why.

## The AI report

Your public IP, computer name and Windows user name are hidden by default. You see exactly what goes into the file before saving, and can switch hiding off.

## Privacy

whynet sends nothing anywhere except what a check needs: DNS-over-HTTPS lookups of the checked domain (1.1.1.1, 8.8.8.8), a request to Cloudflare to learn your public IP and provider, and the page itself. On start it asks GitHub for the latest version number and offers to update; it updates only when you press "Yes" (the new exe is checked against the release's SHA-256). No telemetry. The hidden browser uses a throwaway profile with sign-in and sync switched off, deleted afterwards. A browser record (HAR) is read on your computer only: addresses without queries, type, status, error and timings are taken from it.

## Build from source

Needs Go, Node.js and [Wails v2](https://wails.io).

```
go test ./...                              # tests, including a lab of simulated failures
go run ./cmd/whynet-cli -save youtube.com  # console version, saves the AI report
go run ./cmd/whynet-cli -app [-udp] -for 20s discord  # app check from the console (-udp: UDP too, admin rights)
wails build                                # build/bin/whynet.exe
```

The code is split into probes (collect facts), rules (`rules/diagnoses.yaml` turn facts into a diagnosis) and texts (`locales/ru.yaml`). See [CLAUDE.md](CLAUDE.md) and [docs/](docs/): design, architecture, the catalog of diagnoses, known pitfalls.

## Credits and licenses

MIT, see [LICENSE](LICENSE). Built with [Wails](https://wails.io). The ad and tracker list is [StevenBlack/hosts](https://github.com/StevenBlack/hosts) (MIT, `rules/ads-LICENSE.txt`). Fonts Golos Text and JetBrains Mono (SIL OFL) are bundled via Fontsource.
