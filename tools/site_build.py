#!/usr/bin/env python3
"""Builds site/ (boar.rh1.tech) from the texts below.

    python3 tools/site_build.py && deploy/site.sh

The two languages share one template, so the pages cannot drift apart in
structure; only the words differ. The welcome screen in tools/welcome-screen.html
was rendered once from a real CP437 telnet session by the sysop interface's
ANSI renderer, with the live lines (node, online count, date) cut out.
"""

import pathlib

ROOT = pathlib.Path(__file__).resolve().parent.parent
SCREEN = (ROOT / "tools/welcome-screen.html").read_text().rstrip()
FP = "SHA256:xMvzbhi5k5OnnNhmTObOjBzk6nGVrIRYRJGDP4aPNuI"


def page(lang, t, h1=None):
    return f'''<!DOCTYPE html>
<html lang="{lang}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{t["title"]}</title>
<meta name="description" content="{t["desc"]}">
<link rel="canonical" href="https://boar.rh1.tech{"/" if lang == "en" else "/ru/"}">
<link rel="alternate" hreflang="en" href="https://boar.rh1.tech/">
<link rel="alternate" hreflang="ru" href="https://boar.rh1.tech/ru/">
<link rel="alternate" hreflang="x-default" href="https://boar.rh1.tech/">
<meta name="theme-color" content="#0000aa">
<meta name="color-scheme" content="dark">
<link rel="icon" href="/assets/favicon.png" type="image/png">
<link rel="stylesheet" href="/assets/site.css">
</head>
<body>
<header class="bar">
  <span class="bbs">Boar BBS</span>
  <span class="host">boar.rh1.tech</span>
  <nav aria-label="{t["langnav"]}">
    <a class="item" href="/" hreflang="en" lang="en"{' aria-current="page"' if lang == "en" else ""}><span class="key">E</span>nglish</a>
    <a class="item" href="/ru/" hreflang="ru" lang="ru"{' aria-current="page"' if lang == "ru" else ""}><span class="key">Р</span>усский</a>
  </nav>
</header>

<main>
  <div class="screen">
    <pre role="img" aria-label="{t["screenalt"]}">{SCREEN}</pre>
  </div>

  <h1>{h1 or t["h1"]}</h1>
  <p class="lead">{t["lead"]}</p>

  <section class="box" aria-labelledby="call">
    <h2 id="call">{t["call"]}</h2>
    <div class="way">
      <h3>SSH <span class="tag">{t["recommended"]}</span></h3>
      <p class="cmd">ssh bbs@boar.rh1.tech</p>
      <p>{t["ssh"]}</p>
    </div>
    <div class="way">
      <h3>Telnet</h3>
      <p class="cmd">telnet boar.rh1.tech</p>
      <p>{t["telnet"]}</p>
    </div>
    <div class="way">
      <h3>{t["client"]}</h3>
      <p>{t["clienttext"]}</p>
    </div>
    <p class="dim">{t["fp"]} <span class="fp">{FP}</span> (ED25519)</p>
  </section>

  <div class="split">
    <section class="box" aria-labelledby="first">
      <h2 id="first">{t["first"]}</h2>
      <ol>{"".join(f"<li>{x}</li>" for x in t["steps"])}</ol>
    </section>
    <section class="box" aria-labelledby="inside">
      <h2 id="inside">{t["inside"]}</h2>
      <dl class="inside">{"".join(f"<dt>{a}</dt><dd>{b}</dd>" for a, b in t["things"])}</dl>
    </section>
  </div>

  <section class="box" aria-labelledby="fido">
    <h2 id="fido">FidoNet</h2>
    <p>{t["fido"]}</p>
  </section>

  <section class="box" aria-labelledby="rules">
    <h2 id="rules">{t["rules"]}</h2>
    <p>{t["rulestext"]}</p>
    <p>{t["privacy"]}</p>
  </section>
</main>

<footer>
  <p>{t["foot"]}</p>
  <p>© 2026 Mikhail Matveev · <a href="https://github.com/rh1tech/boar">github.com/rh1tech/boar</a> · GPL-3.0</p>
</footer>
</body>
</html>
'''


EN = dict(
    title="Boar BBS: a bulletin board you call over SSH or telnet",
    desc="Boar BBS is an old-style bulletin board: private mail, message boards, chat, a door game and ANSI art. Call it with ssh bbs@boar.rh1.tech or telnet boar.rh1.tech.",
    langnav="Language",
    screenalt="The Boar BBS login screen: the word BOAR in white and yellow block letters, a framed banner reading Boar BBS, the wild boar, private mail, est. 2026, and the prompt Handle (or NEW to register).",
    h1="A bulletin board you call from a terminal.",
    lead="Boar is an old-style BBS that runs today. You connect with SSH or telnet, pick a handle, and you're in. There's private mail, message boards, live chat, a door game and a wall of one-liners, all in ANSI colour with CP437 block art.",
    call="How to call", recommended="encrypted",
    ssh="Any user name works: you sign in to the BBS itself after connecting. Once you have an account you can add your SSH key under Settings, and then this command logs you straight in.",
    telnet="Port 23. Telnet sends everything unencrypted, your password included, so use SSH when you can.",
    client="A BBS client",
    clienttext="SyncTERM, NetRunner or any other ANSI-BBS client works. Add <code>boar.rh1.tech</code>, telnet on port 23 or SSH on port 22, and pick option 2 (CP437) when the BBS asks about your terminal.",
    fp="The first time SSH asks about the server's key, check that it's",
    first="Your first call",
    steps=[
        "Choose your terminal: <b>1</b> for UTF-8 (macOS Terminal, iTerm2, PuTTY), <b>2</b> for CP437 (SyncTERM and other BBS clients), <b>3</b> for plain ASCII.",
        "Type <b>NEW</b> at the handle prompt, then choose a handle and a password of at least 8 characters.",
        "Until a sysop approves the account you can read the boards and the news and mail a sysop. Once you're approved, everything else opens up.",
        "Use a window at least 80 columns wide. Long output pauses at <code>-- more --</code>.",
    ],
    inside="What's inside",
    things=[
        ("Mail", "Private messages with threads, quoting and search. One message can go to several callers."),
        ("Boards", "Public message boards, with a scan for everything new since your last call."),
        ("Chat", "Live chat rooms, and one-line pages to anyone who's online."),
        ("Doors", "Boar Hunt, a small door game."),
        ("The wall", "One-liners shown at login, and news from the sysop."),
        ("Email", "If you want it, a notice or a copy of new mail, sent only to an address you've verified."),
    ],
    fido="Boar has applied to join FidoNet, the network BBSes have used since the 1980s to exchange mail. Once it's listed, you'll be able to send netmail and read echomail from the BBS.",
    rules="House rules",
    rulestext="Be decent to other callers. No spam, no advertising, and no attempts to break the system. A sysop can lock accounts that don't play along.",
    privacy="The BBS keeps your handle, a hash of your password, your messages and a log of logins. This page sets no cookies and loads nothing from other sites.",
    foot="Boar BBS is free software. Questions? Call the BBS and mail a sysop.",
)

RU = dict(
    title="Boar BBS: BBS, на которую заходят по SSH и telnet",
    desc="Boar BBS — BBS в духе девяностых: личная почта, конференции, чат, игра и ANSI-графика. Подключение: ssh bbs@boar.rh1.tech или telnet boar.rh1.tech.",
    langnav="Язык",
    screenalt="Экран входа Boar BBS: надпись BOAR крупными блочными буквами, рамка с заголовком Boar BBS и приглашение Handle (or NEW to register).",
    h1="BBS, на которую заходят из терминала.",
    lead="Boar — BBS в духе девяностых, которая работает и сегодня. Подключитесь по SSH или telnet, зарегистрируйтесь — и заходите. Внутри — личная почта, конференции, чат, игра Boar Hunt и стена коротких сообщений. Оформление — ANSI-цвет и псевдографика CP437.",
    call="Как подключиться", recommended="шифрование",
    ssh="Имя пользователя может быть любым: на саму BBS вы входите уже после подключения. Когда заведёте учётную запись, добавьте свой SSH-ключ в настройках — и эта команда будет пускать сразу, без пароля.",
    telnet="Порт 23. Telnet передаёт всё в открытом виде, включая пароль, поэтому лучше пользоваться SSH.",
    client="BBS-клиент",
    clienttext="Подойдёт SyncTERM, NetRunner или любая другая программа для ANSI-BBS. Укажите адрес <code>boar.rh1.tech</code>, telnet на порту 23 или SSH на порту 22, а на вопрос о терминале ответьте 2 (CP437).",
    fp="При первом подключении SSH покажет отпечаток ключа сервера. Он должен совпадать с этим:",
    first="Первый вход",
    steps=[
        "Выберите терминал: <b>1</b> — UTF-8 (Терминал macOS, iTerm2, PuTTY), <b>2</b> — CP437 (SyncTERM и другие BBS-клиенты), <b>3</b> — обычный ASCII без цвета.",
        "На приглашении Handle введите <b>NEW</b>, придумайте ник и пароль не короче 8 символов.",
        "Пока сисоп не подтвердит регистрацию, можно читать конференции и новости и писать сисопу. После подтверждения открывается всё остальное.",
        "Окно терминала должно быть шириной не меньше 80 символов. Длинный текст выводится постранично, с паузой на <code>-- more --</code>.",
    ],
    inside="Что здесь есть",
    things=[
        ("Почта", "Личная переписка с цепочками писем, цитированием и поиском. Одно письмо можно отправить сразу нескольким адресатам."),
        ("Конференции", "Открытые обсуждения. Всё новое с прошлого визита можно прочитать разом."),
        ("Чат", "Комнаты для живого общения и короткие сообщения тем, кто сейчас на линии."),
        ("Игра", "Boar Hunt — небольшая door-игра."),
        ("Стена", "Короткие сообщения, которые все видят при входе, и новости от сисопа."),
        ("Email", "По желанию — уведомление или копия новых писем, только на подтверждённый адрес."),
    ],
    fido="Boar подала заявку на вступление в FidoNet — сеть, через которую BBS обмениваются почтой с 1980-х годов. Когда узел появится в нодлисте, с BBS можно будет писать нетмейл и читать эхоконференции.",
    rules="Правила",
    rulestext="Уважайте других участников. Никакого спама, рекламы и попыток взломать систему. Сисоп может заблокировать учётную запись нарушителя.",
    privacy="BBS хранит ваш ник, хеш пароля, ваши сообщения и журнал входов. Эта страница не использует cookies и ничего не загружает со сторонних сайтов.",
    foot="Boar BBS — свободное программное обеспечение. Есть вопросы? Зайдите на BBS и напишите сисопу.",
)

(ROOT / "site/index.html").write_text(page("en", EN))
(ROOT / "site/ru").mkdir(exist_ok=True)
(ROOT / "site/ru/index.html").write_text(page("ru", RU))
(ROOT / "site/404.html").write_text(page("en", EN, h1="NO CARRIER: that page isn't here."))
print("site/ built")
