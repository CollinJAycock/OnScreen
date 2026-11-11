' Unit tests for source/util/I18n.brs: which language, the lookup, the
' placeholders and plurals, and Spanish through a few helpers that use it
' (WatchState, DetailMeta). brs has no global node, so the catalogs are
' read from locale/ and m.i18nLang picks the language.

sub Main()
    ' Which language.
    runCase("resolve: choice es", I18n_Resolve("es", "en_US"), "es")
    runCase("resolve: choice en over a Spanish Roku", I18n_Resolve("en", "es_MX"), "en")
    runCase("resolve: no choice, es_MX", I18n_Resolve(invalid, "es_MX"), "es")
    runCase("resolve: empty choice, es_ES", I18n_Resolve("", "es_ES"), "es")
    runCase("resolve: no choice, en_US", I18n_Resolve("", "en_US"), "en")
    runCase("resolve: Chinese isn't shipped", I18n_Resolve("zh-Hans", "en_US"), "en")
    runCase("resolve: other locale", I18n_Resolve(invalid, "fr_CA"), "en")
    runCase("resolve: nothing known", I18n_Resolve(invalid, invalid), "en")
    runCase("account: es", I18n_AccountChoice("es"), "es")
    runCase("account: zh-Hans is none", I18n_AccountChoice("zh-Hans"), "")
    runCase("account: null is none", I18n_AccountChoice(invalid), "")
    runCase("account: not a string", I18n_AccountChoice(3), "")
    runCase("name: en", I18n_LanguageName("en"), "English")
    runCase("name: es", I18n_LanguageName("es"), "Español")

    ' Catalogs.
    en = I18n_ReadCatalog("en")
    es = I18n_ReadCatalog("es")
    runBool("catalog: en read", en.Count() > 100, true)
    runCase("catalog: es has the same keys", es.Count().ToStr(), en.Count().ToStr())

    ' English (the default).
    runCase("lang: default", I18n_Lang(), "en")
    runCase("t: en", I18n_T("nav.settings"), "Settings")
    runCase("t: missing key shows the key", I18n_T("nav.nope"), "nav.nope")
    runCase("tp: fills", I18n_TP("settings.signedInAs", { username: "ana" }), "Signed in as ana")
    runCase("tp: number", I18n_TP("item.runtimeHours", { hours: 2, minutes: 5 }), "2h 5m")
    runCase("tp: unknown param ignored", I18n_TP("nav.search", { x: "y" }), "Search")
    runCase("tn: one", I18n_TN("search.typeMore", 1), "Type at least 1 character.")
    runCase("tn: other", I18n_TN("search.typeMore", 2), "Type at least 2 characters.")
    runCase("tn: params", I18n_TN("item.versionParts", 2, { version: "4K" }), "4K · 2 parts")
    runCase("fill: a key's case", I18n_Fill("a {name} b", { NAME: "x" }), "a x b")
    runCase("fill: every occurrence", I18n_Fill("{n}+{n}", { n: 1 }), "1+1")
    runCase("fill: no params", I18n_Fill("a {n}", invalid), "a {n}")
    runCase("decimal: en", I18n_Decimal("7.5"), "7.5")
    runCase("en: up next", WatchState_UpNextAction({ mode: "resume", episode: { id: "e", season_number: 1, episode_number: 2 } }).label, "Resume S1 · E2")

    ' Spanish.
    m.i18nLang = "es"
    runCase("lang: es", I18n_Lang(), "es")
    runCase("es: t", I18n_T("nav.settings"), "Ajustes")
    runCase("es: tp", I18n_TP("settings.signedInAs", { username: "ana" }), "Sesión iniciada como ana")
    runCase("es: tn one", I18n_TN("search.hidden", 1), "1 coincidencia oculta por los filtros de tipo de arriba.")
    runCase("es: tn other", I18n_TN("search.hidden", 3), "3 coincidencias ocultas por los filtros de tipo de arriba.")
    runCase("es: decimal", I18n_Decimal("1.25"), "1,25")
    runCase("es: up next", WatchState_UpNextAction({ mode: "resume", episode: { id: "e", season_number: 1, episode_number: 2 } }).label, "Reanudar S1 · E2")
    runCase("es: rewatch", WatchState_UpNextAction({ mode: "rewatch", episode: { id: "e" } }).label, "Ver de nuevo")
    runCase("es: meta line", DetailMeta_Line({ year: 2019, duration_ms: 7920000, rating: 7.5 }), "2019  ·  2 h 12 min  ·  ★  7,5")
    runCase("es: unwatch show", WatchState_ConfirmUnwatchShowText("Lost"), "¿Marcar todos los episodios de “Lost” como no vistos? Esto borra tus marcas de visto y los puntos de reanudación de toda la serie.")

    ' Back to English: the cached catalog follows the language.
    m.i18nLang = "en"
    runCase("back to en", I18n_T("nav.settings"), "Settings")
    runCase("en: meta line", DetailMeta_Line({ year: 2019, duration_ms: 7920000, rating: 7.5 }), "2019  ·  2h 12m  ·  ★  7.5")

    print "DONE: I18n_test"
end sub

sub runCase(name as String, actual as String, expected as String)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=[" + expected + "] actual=[" + actual + "]"
    end if
end sub

sub runBool(name as String, actual as Boolean, expected as Boolean)
    if actual = expected
        print "PASS: " + name
    else
        print "FAIL: " + name + " — expected=" + expected.ToStr() + " actual=" + actual.ToStr()
    end if
end sub
