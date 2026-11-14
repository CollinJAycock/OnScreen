"""The add-on's strings, and the generator for its Kodi .po files.

One table, three languages: English (resource.language.en_gb), Spanish
(es_es) and Simplified Chinese (zh_cn), the languages the rest of OnScreen
ships (docs/i18n.md). The Spanish and Chinese follow the wording of the web
app's catalogs (web/src/lib/i18n/locales); like those, they're drafts until
a fluent reviewer has read them.

    python clients/kodi/scripts/strings.py          # rewrite the .po files
    python clients/kodi/scripts/strings.py --check  # fail if they're stale

{name} placeholders are filled by resources/lib/kodi/common.py's t(); every
translation must use the same ones (tests/test_strings.py checks).
"""

import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
LANG_DIR = os.path.join(HERE, "..", "plugin.video.onscreen", "resources", "language")

# (id, English, Spanish, Simplified Chinese)
STRINGS = [
    # ── settings ──
    (30001, "Server address", "Dirección del servidor", "服务器地址"),
    (
        30002,
        "The address of your OnScreen server, such as http://192.168.1.10:7070 or https://media.example.com.",
        "La dirección de tu servidor OnScreen, por ejemplo http://192.168.1.10:7070 o https://media.example.com.",
        "你的 OnScreen 服务器地址，例如 http://192.168.1.10:7070 或 https://media.example.com。",
    ),
    (30003, "Sign in", "Iniciar sesión", "登录"),
    (30004, "Sign out", "Cerrar sesión", "退出登录"),
    (30005, "Change server…", "Cambiar de servidor…", "更换服务器…"),
    (30006, "Server", "Servidor", "服务器"),
    (30007, "Server and account", "Servidor y cuenta", "服务器和账户"),
    (30010, "Playback", "Reproducción", "播放"),
    (30011, "What this device plays", "Lo que reproduce este dispositivo", "此设备可播放的内容"),
    (30012, "Highest resolution", "Resolución máxima", "最高分辨率"),
    (
        30013,
        "The server scales larger videos down to this.",
        "El servidor reduce a esta resolución los vídeos más grandes.",
        "服务器会把更大的视频缩小到此分辨率。",
    ),
    (30014, "4K (2160p)", "4K (2160p)", "4K (2160p)"),
    (30015, "Full HD (1080p)", "Full HD (1080p)", "全高清 (1080p)"),
    (30016, "HD (720p)", "HD (720p)", "高清 (720p)"),
    (30017, "Play HEVC (H.265)", "Reproducir HEVC (H.265)", "播放 HEVC (H.265)"),
    (
        30018,
        "Turn off if HEVC video stutters on this device. The server then converts it to H.264.",
        "Desactívalo si el vídeo HEVC se entrecorta en este dispositivo. El servidor lo convertirá a H.264.",
        "如果 HEVC 视频在此设备上卡顿，请关闭此项。服务器会将其转换为 H.264。",
    ),
    (30019, "Play AV1", "Reproducir AV1", "播放 AV1"),
    (
        30020,
        "Turn off if AV1 video stutters on this device (older hardware decodes it in software).",
        "Desactívalo si el vídeo AV1 se entrecorta en este dispositivo (el hardware antiguo lo decodifica por software).",
        "如果 AV1 视频在此设备上卡顿，请关闭此项（较旧的硬件以软件解码）。",
    ),
    (30021, "Play VP9", "Reproducir VP9", "播放 VP9"),
    (
        30022,
        "Turn off if VP9 video stutters on this device.",
        "Desactívalo si el vídeo VP9 se entrecorta en este dispositivo.",
        "如果 VP9 视频在此设备上卡顿，请关闭此项。",
    ),
    (30023, "HDR", "HDR", "HDR"),
    (30024, "HDR video", "Vídeo HDR", "HDR 视频"),
    (
        30025,
        "Automatic plays HDR as HDR when the display takes it and asks the server to convert it to SDR otherwise.",
        "Automático reproduce el HDR como HDR si la pantalla lo admite y, si no, pide al servidor que lo convierta a SDR.",
        "“自动”会在显示器支持时以 HDR 播放 HDR，否则请服务器将其转换为 SDR。",
    ),
    (30026, "Automatic (follow the display)", "Automático (según la pantalla)", "自动（跟随显示器）"),
    (
        30027,
        "Play as HDR (Kodi converts it if needed)",
        "Reproducir como HDR (Kodi lo convierte si hace falta)",
        "以 HDR 播放（必要时由 Kodi 转换）",
    ),
    (30028, "Have the server convert it to SDR", "Que el servidor lo convierta a SDR", "由服务器转换为 SDR"),
    (30029, "Dolby Vision", "Dolby Vision", "Dolby Vision"),
    (
        30030,
        "Automatic plays Dolby Vision when the display takes it.",
        "Automático reproduce Dolby Vision si la pantalla lo admite.",
        "“自动”会在显示器支持时播放 Dolby Vision。",
    ),
    (30031, "Off", "Desactivado", "关闭"),
    (30032, "Versions", "Versiones", "版本"),
    (
        30033,
        "Ask which version to play when there are several",
        "Preguntar qué versión reproducir cuando haya varias",
        "有多个版本时询问要播放哪个",
    ),
    (
        30034,
        "Otherwise the best version plays. Play version… in the context menu always asks.",
        "Si no, se reproduce la mejor versión. Reproducir versión… en el menú contextual siempre pregunta.",
        "否则播放最佳版本。上下文菜单中的“播放版本…”总会询问。",
    ),
    (30035, "Other devices", "Otros dispositivos", "其他设备"),
    (
        30036,
        'Receive "Play on…" from other OnScreen apps',
        'Recibir "Reproducir en…" desde otras aplicaciones de OnScreen',
        "接收来自其他 OnScreen 应用的“在…上播放”",
    ),
    (
        30037,
        "This Kodi checks with the server every few seconds for a title sent to it from a phone or the web.",
        "Este Kodi consulta al servidor cada pocos segundos si le han enviado un título desde un teléfono o la web.",
        "此 Kodi 每隔几秒向服务器查询是否有从手机或网页发送过来的标题。",
    ),
    (
        30038,
        "Offer to install InputStream Adaptive",
        "Ofrecer instalar InputStream Adaptive",
        "提示安装 InputStream Adaptive",
    ),
    (
        30039,
        "Kodi needs it to seek in video the server converts. The offer comes once; turn this on to see it again.",
        "Kodi lo necesita para avanzar o retroceder en el vídeo que convierte el servidor. Se ofrece una vez; actívalo para volver a verlo.",
        "Kodi 需要它才能在服务器转换的视频中跳转。该提示只出现一次；开启此项可再次显示。",
    ),
    (30040, "Advanced", "Avanzado", "高级"),
    (30041, "Items per page", "Elementos por página", "每页项目数"),
    (
        30042,
        "How many titles a library page lists before Next page.",
        "Cuántos títulos muestra una página de la biblioteca antes de Página siguiente.",
        "媒体库每页在“下一页”之前列出的标题数。",
    ),
    (
        30043,
        "Load artwork through a local proxy",
        "Cargar las imágenes a través de un proxy local",
        "通过本地代理加载图片",
    ),
    (
        30044,
        "Keeps poster addresses the same from day to day, so Kodi downloads each image once. Turn off only if the proxy can't start.",
        "Mantiene las mismas direcciones de las carátulas de un día a otro, para que Kodi descargue cada imagen una sola vez. Desactívalo solo si el proxy no puede iniciarse.",
        "让海报地址每天保持不变，使 Kodi 每张图片只下载一次。仅在代理无法启动时关闭。",
    ),
    (30045, "Proxy port", "Puerto del proxy", "代理端口"),
    (
        30046,
        "The local port the artwork proxy listens on (127.0.0.1 only). Changing it makes Kodi download the artwork again.",
        "El puerto local en el que escucha el proxy de imágenes (solo 127.0.0.1). Si lo cambias, Kodi volverá a descargar las imágenes.",
        "图片代理监听的本地端口（仅限 127.0.0.1）。更改后 Kodi 会重新下载图片。",
    ),
    # ── menus ──
    (30100, "Continue Watching", "Seguir viendo", "继续观看"),
    (30101, "Next Up", "A continuación", "接下来"),
    (30102, "Recently Added", "Añadido recientemente", "最近添加"),
    (30103, "Plan to Watch", "Por ver", "计划观看"),
    (30104, "Trending this week", "Tendencias de la semana", "本周热门"),
    (30105, "Libraries", "Bibliotecas", "媒体库"),
    (30106, "Watchlist", "Mi lista", "待看清单"),
    (30107, "Favorites", "Favoritos", "收藏"),
    (30108, "Search", "Buscar", "搜索"),
    (30109, "Settings", "Ajustes", "设置"),
    (30110, "Next page ({shown} of {total})", "Página siguiente ({shown} de {total})", "下一页（{shown} / {total}）"),
    (30111, "Signed in as {user}", "Sesión iniciada como {user}", "已登录为 {user}"),
    (30112, "Not signed in", "Sin iniciar sesión", "未登录"),
    (30113, "Collections", "Colecciones", "合集"),
    (30114, "Play", "Reproducir", "播放"),
    (30115, "All", "Todo", "全部"),
    (30116, "Unwatched", "No vistos", "未观看"),
    (30117, "In progress", "En curso", "观看中"),
    (30118, "Recently added", "Añadidos recientemente", "最近添加"),
    (30119, "Albums", "Álbumes", "专辑"),
    (30120, "Season {n}", "Temporada {n}", "第 {n} 季"),
    (30121, "Specials", "Especiales", "特别篇"),
    (30122, "Episode {n}", "Episodio {n}", "第 {n} 集"),
    (30123, "Version {n}", "Versión {n}", "版本 {n}"),
    (30124, "{version} · {count} parts", "{version} · {count} partes", "{version} · {count} 个部分"),
    (30125, "Choose a version", "Elige una versión", "选择版本"),
    (30126, "Books", "Libros", "图书"),
    (30130, "Play with options…", "Reproducir con opciones…", "带选项播放…"),
    (30131, "Mark watched", "Marcar como visto", "标记为已看"),
    (30132, "Mark unwatched", "Marcar como no visto", "标记为未看"),
    (30133, "Add to Watchlist", "Añadir a Mi lista", "添加到待看清单"),
    (30134, "Remove from Watchlist", "Quitar de Mi lista", "从待看清单中移除"),
    (30135, "Add to favorites", "Añadir a favoritos", "添加到收藏"),
    (30136, "Remove from favorites", "Quitar de favoritos", "从收藏中移除"),
    (30137, "Remove from Continue Watching", "Quitar de Seguir viendo", "从“继续观看”中移除"),
    (30138, "Go to show", "Ir a la serie", "前往剧集"),
    (30140, "Resume from {time}", "Reanudar desde {time}", "从 {time} 继续播放"),
    (30141, "Play from the beginning", "Reproducir desde el principio", "从头播放"),
    (30142, "Track {n}", "Pista {n}", "音轨 {n}"),
    (30143, "Off", "Desactivados", "关闭"),
    (30144, "forced", "forzados", "强制"),
    (30145, "burned in", "incrustados", "烧录"),
    (30146, "external", "externos", "外挂"),
    (30147, "Audio", "Audio", "音频"),
    (30148, "Subtitles", "Subtítulos", "字幕"),
    (30150, "Live TV", "TV en directo", "电视直播"),
    (30151, "Channels", "Canales", "频道"),
    (30152, "Recordings", "Grabaciones", "录像"),
    (30153, "Now: {title} ({time})", "Ahora: {title} ({time})", "正在播放：{title}（{time}）"),
    (30154, "Next: {title} ({time})", "A continuación: {title} ({time})", "接下来：{title}（{time}）"),
    (30155, "Scheduled", "Programada", "已计划"),
    (30156, "Recording now", "Grabando", "正在录制"),
    (30157, "Failed", "Fallida", "失败"),
    (30158, "Cancelled", "Cancelada", "已取消"),
    (30159, "Replaced by a newer recording", "Sustituida por una grabación más reciente", "已被较新的录像取代"),
    # ── sign-in ──
    (
        30200,
        "Server address (e.g. http://192.168.1.10:7070)",
        "Dirección del servidor (p. ej., http://192.168.1.10:7070)",
        "服务器地址（例如 http://192.168.1.10:7070）",
    ),
    (30201, "Couldn't reach {url}: {reason}", "No se pudo conectar con {url}: {reason}", "无法连接到 {url}：{reason}"),
    (
        30202,
        "{url} answered, but it isn't an OnScreen server.",
        "{url} respondió, pero no es un servidor OnScreen.",
        "{url} 有响应，但它不是 OnScreen 服务器。",
    ),
    (30203, "Sign in to OnScreen", "Iniciar sesión en OnScreen", "登录 OnScreen"),
    (
        30204,
        "On a phone or computer, open",
        "En un teléfono o un ordenador, abre",
        "请在手机或电脑上打开",
    ),
    (30205, "and enter the PIN", "y escribe el PIN", "并输入 PIN 码"),
    (
        30206,
        "The PIN expired. Getting a new one…",
        "El PIN ha caducado. Generando otro…",
        "PIN 码已过期。正在获取新的…",
    ),
    (
        30207,
        "Couldn't start pairing: {reason}",
        "No se pudo iniciar la vinculación: {reason}",
        "无法开始配对：{reason}",
    ),
    (30208, "How do you want to sign in?", "¿Cómo quieres iniciar sesión?", "你想如何登录？"),
    (
        30209,
        "With a PIN from a phone or computer (single sign-on too)",
        "Con un PIN desde un teléfono o un ordenador (también inicio de sesión único)",
        "使用手机或电脑上的 PIN 码（也支持单点登录）",
    ),
    (30210, "With a username and password", "Con usuario y contraseña", "使用用户名和密码"),
    (30211, "Username", "Nombre de usuario", "用户名"),
    (30212, "Password", "Contraseña", "密码"),
    (
        30213,
        "Two-factor code (or a recovery code)",
        "Código de verificación en dos pasos (o un código de recuperación)",
        "两步验证码（或恢复码）",
    ),
    (30214, "Sign-in failed: {reason}", "No se pudo iniciar sesión: {reason}", "登录失败：{reason}"),
    (30215, "Signed in as {user}", "Sesión iniciada como {user}", "已登录为 {user}"),
    (30216, "Signed out", "Sesión cerrada", "已退出登录"),
    (
        30217,
        "Sign {user} out on this device?",
        "¿Cerrar la sesión de {user} en este dispositivo?",
        "要在此设备上退出 {user} 的登录吗？",
    ),
    (
        30218,
        "Your sign-in has expired. Sign in again.",
        "Tu sesión ha caducado. Vuelve a iniciar sesión.",
        "你的登录已过期。请重新登录。",
    ),
    # ── playback ──
    (30300, "Too many streams", "Demasiadas reproducciones", "播放流过多"),
    (
        30301,
        "Your account is already playing as many streams as it may. Stop one and try again.",
        "Tu cuenta ya está reproduciendo todas las transmisiones que puede. Detén una y vuelve a intentarlo.",
        "你的账户正在播放的串流已达上限。请停止其中一个后再试。",
    ),
    (
        30302,
        "An administrator stopped this stream: {message}",
        "Un administrador detuvo esta reproducción: {message}",
        "管理员停止了此串流：{message}",
    ),
    (
        30303,
        "An administrator stopped this stream.",
        "Un administrador detuvo esta reproducción.",
        "管理员停止了此串流。",
    ),
    (30304, "Watch limit reached", "Límite de visualización alcanzado", "已达到观看限制"),
    (
        30305,
        "Playback is outside the allowed hours set for this account. Try again during the permitted times.",
        "La reproducción está fuera del horario permitido para esta cuenta. Inténtalo de nuevo en el horario permitido.",
        "当前不在此账户允许的观看时段内。请在允许的时间内再试。",
    ),
    (
        30306,
        "This account has used up today's watch time.",
        "Esta cuenta ya ha agotado el tiempo de visualización de hoy.",
        "此账户今天的观看时长已用完。",
    ),
    (
        30307,
        "Playback is blocked by a parental watch limit on this account.",
        "La reproducción está bloqueada por un límite de control parental en esta cuenta.",
        "此账户的家长观看限制阻止了播放。",
    ),
    (
        30308,
        "Dolby Vision is not supported: this video can't play on this device, and the server can't convert it.",
        "Dolby Vision no es compatible: este vídeo no se puede reproducir en este dispositivo y el servidor no puede convertirlo.",
        "不支持 Dolby Vision：此视频无法在此设备上播放，服务器也无法转换。",
    ),
    (
        30309,
        "This file appears to be damaged, so it can't be played.",
        "Este archivo parece estar dañado, así que no se puede reproducir.",
        "此文件似乎已损坏，无法播放。",
    ),
    (30310, "Couldn't start playback.", "No se pudo iniciar la reproducción.", "无法开始播放。"),
    (
        30311,
        "This title has no playable files.",
        "Este título no tiene archivos reproducibles.",
        "此标题没有可播放的文件。",
    ),
    (30312, "Starting…", "Iniciando…", "正在启动…"),
    (30313, "Something went wrong.", "Algo ha salido mal.", "出错了。"),
    (30314, "Done", "Hecho", "完成"),
    (30315, "Nothing here.", "No hay nada aquí.", "这里什么都没有。"),
    (
        30316,
        "The server can't burn subtitles into the picture right now, so this plays without them.",
        "El servidor no puede incrustar subtítulos en la imagen ahora mismo, así que se reproduce sin ellos.",
        "服务器目前无法将字幕烧录到画面中，因此将不带字幕播放。",
    ),
    (
        30317,
        "Subtitles can't be burned into a movie in several parts, so this plays without them.",
        "No se pueden incrustar subtítulos en una película de varias partes, así que se reproduce sin ellos.",
        "无法将字幕烧录到分为多个部分的电影中，因此将不带字幕播放。",
    ),
    (
        30318,
        "Couldn't burn in that subtitle, so this plays without it.",
        "No se pudo incrustar ese subtítulo, así que se reproduce sin él.",
        "无法烧录该字幕，因此将不带字幕播放。",
    ),
    (
        30319,
        "To seek in converted video, Kodi needs InputStream Adaptive. Install it when Kodi asks.",
        "Para avanzar o retroceder en un vídeo convertido, Kodi necesita InputStream Adaptive. Instálalo cuando Kodi lo pregunte.",
        "要在转换后的视频中跳转，Kodi 需要 InputStream Adaptive。Kodi 询问时请安装。",
    ),
]

LANGS = (
    ("resource.language.en_gb", "en_GB", "English", 1),
    ("resource.language.es_es", "es_ES", "Spanish", 2),
    ("resource.language.zh_cn", "zh_CN", "Chinese (Simple)", 3),
)

HEADER = """# Kodi Media Center language file
# Addon Name: OnScreen
# Addon id: plugin.video.onscreen
# Addon Provider: OnScreen
# Generated by clients/kodi/scripts/strings.py; edit the table there.
msgid ""
msgstr ""
"Project-Id-Version: plugin.video.onscreen\\n"
"Report-Msgid-Bugs-To: https://github.com/CollinJAycock/OnScreen/issues\\n"
"MIME-Version: 1.0\\n"
"Content-Type: text/plain; charset=UTF-8\\n"
"Content-Transfer-Encoding: 8bit\\n"
"Language: {lang}\\n"
"Plural-Forms: nplurals=2; plural=(n != 1);\\n"
"""


def _q(s):
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def render(column, lang):
    out = [HEADER.replace("{lang}", lang)]
    for row in STRINGS:
        sid, en = row[0], row[1]
        msgstr = "" if column == 1 else row[column]
        out.append('\nmsgctxt "#%d"\nmsgid %s\nmsgstr %s\n' % (sid, _q(en), _q(msgstr)))
    return "".join(out)


def targets():
    for folder, lang, _name, column in LANGS:
        yield os.path.join(LANG_DIR, folder, "strings.po"), render(column, lang)


CRLF = "\r\n"


def main(argv):
    check = "--check" in argv
    stale = []
    for path, text in targets():
        try:
            with open(path, encoding="utf-8", newline="") as f:
                # A Windows checkout (core.autocrlf) has CRLF; that's not stale.
                current = f.read().replace(CRLF, "\n")
        except OSError:
            current = None
        if current == text:
            continue
        if check:
            stale.append(path)
            continue
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w", encoding="utf-8", newline="\n") as f:
            f.write(text)
        print("wrote", os.path.relpath(path))
    if stale:
        print("stale (run python clients/kodi/scripts/strings.py):\n  " + "\n  ".join(stale))
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
