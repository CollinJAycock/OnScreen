' Unit tests for source/util/SettingsPrefs.brs.

sub Main()
    langs = SettingsPrefs_LangOptions()
    runCase("lang: none", SettingsPrefs_IndexOf(langs, invalid).ToStr(), "0")
    runCase("lang: empty", SettingsPrefs_IndexOf(langs, "").ToStr(), "0")
    runCase("lang: exact", langs[SettingsPrefs_IndexOf(langs, "deu")].label, "German")
    runCase("lang: fre is French", langs[SettingsPrefs_IndexOf(langs, "fre")].label, "French")
    runCase("lang: fr-CA is French", langs[SettingsPrefs_IndexOf(langs, "fr-CA")].label, "French")
    runCase("lang: unknown", SettingsPrefs_IndexOf(langs, "xyz").ToStr(), "0")
    subs = SettingsPrefs_SubOptions()
    runCase("subs: off first", subs[0].label, "Off")
    runCase("subs: off stored", subs[SettingsPrefs_IndexOf(subs, "off")].label, "Off")
    runCase("subs: then auto", subs[1].label, "Auto / file default")

    rows = SettingsPrefs_QualityRows()
    runCase("quality: rows", rows.Count().ToStr(), "4")
    res = rows[0].options
    runCase("quality: 1080 stored", res[SettingsPrefs_IndexOf(res, 1080)].label, "1080p")
    runCase("quality: 0 is none", SettingsPrefs_IndexOf(res, 0).ToStr(), "0")
    runCase("quality: 8 Mbps", rows[1].options[SettingsPrefs_IndexOf(rows[1].options, 8000)].label, "8 Mbps")
    runCase("quality: hevc", rows[3].options[SettingsPrefs_IndexOf(rows[3].options, "hevc")].label, "HEVC (smaller, where supported)")
    runCase("next: wraps", SettingsPrefs_Next(res, res.Count() - 1).ToStr(), "0")

    lb = SettingsPrefs_LangBody("eng", "")
    runCase("lang body: audio", lb.preferred_audio_lang, "eng")
    runBool("lang body: none is null", lb.preferred_subtitle_lang = invalid, true)
    qb = SettingsPrefs_QualityBody({ max_video_height: 1080, preferred_video_codec: "h264" }, true)
    runBool("quality body: forced", qb.forced_subtitles_only, true)
    runCase("quality body: height", qb.max_video_height.ToStr(), "1080")
    runBool("quality body: unset is null", qb.max_video_bitrate_kbps = invalid, true)
    runBool("quality body: every field present", qb.DoesExist("max_audio_bitrate_kbps"), true)

    runCase("label: matched", SettingsPrefs_ValueLabel(res, 720, "max_video_height"), "720p")
    runCase("label: none", SettingsPrefs_ValueLabel(res, invalid, "max_video_height"), "Original")
    runCase("label: other height", SettingsPrefs_ValueLabel(res, 1440, "max_video_height"), "1440p")
    runCase("label: other bitrate", SettingsPrefs_ValueLabel(rows[1].options, 5500, "max_video_bitrate_kbps"), "5.5 Mbps")
    runCase("label: other audio", SettingsPrefs_ValueLabel(rows[2].options, 160, "max_audio_bitrate_kbps"), "160 kbps")
    runCase("label: other language", SettingsPrefs_ValueLabel(langs, "nld", "lang"), "nld")
    runCase("label: language alias", SettingsPrefs_ValueLabel(langs, "ger", "lang"), "German")

    au = [{ language: "eng", default: true }, { language: "jpn" }, { language: "fre" }]
    runCase("audio: no preference", SettingsPrefs_AudioPosition(au, "").ToStr(), "-1")
    runCase("audio: default already matches", SettingsPrefs_AudioPosition(au, "en").ToStr(), "-1")
    runCase("audio: another track", SettingsPrefs_AudioPosition(au, "jpn").ToStr(), "1")
    runCase("audio: alias", SettingsPrefs_AudioPosition(au, "fra").ToStr(), "2")
    runCase("audio: none in that language", SettingsPrefs_AudioPosition(au, "deu").ToStr(), "-1")
    runCase("audio: one track", SettingsPrefs_AudioPosition([{ language: "jpn" }], "eng").ToStr(), "-1")
    runCase("audio: default flag elsewhere", SettingsPrefs_AudioPosition([{ language: "eng" }, { language: "jpn", default: true }], "eng").ToStr(), "0")

    print "DONE: SettingsPrefs_test"
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
