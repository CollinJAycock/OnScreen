package subtitles

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// iso639_2to1 maps ISO 639-2 codes (both the bibliographic and terminology
// forms) to ISO 639-1. ffprobe reports an embedded stream's language in
// 639-2 ("eng"); OpenSubtitles and the auto-download setting use 639-1
// ("en"). Covers the languages OpenSubtitles carries in any volume.
var iso639_2to1 = map[string]string{
	"eng": "en", "spa": "es", "fre": "fr", "fra": "fr", "ger": "de", "deu": "de",
	"ita": "it", "por": "pt", "dut": "nl", "nld": "nl", "swe": "sv", "nor": "no",
	"nob": "no", "nno": "no", "dan": "da", "fin": "fi", "ice": "is", "isl": "is",
	"pol": "pl", "cze": "cs", "ces": "cs", "slo": "sk", "slk": "sk", "slv": "sl",
	"hun": "hu", "rum": "ro", "ron": "ro", "bul": "bg", "hrv": "hr", "srp": "sr",
	"bos": "bs", "mac": "mk", "mkd": "mk", "alb": "sq", "sqi": "sq", "gre": "el",
	"ell": "el", "tur": "tr", "rus": "ru", "ukr": "uk", "bel": "be", "est": "et",
	"lav": "lv", "lit": "lt", "ara": "ar", "heb": "he", "per": "fa", "fas": "fa",
	"hin": "hi", "ben": "bn", "tam": "ta", "tel": "te", "mal": "ml", "kan": "kn",
	"mar": "mr", "urd": "ur", "tha": "th", "vie": "vi", "ind": "id", "may": "ms",
	"msa": "ms", "fil": "tl", "tgl": "tl", "chi": "zh", "zho": "zh", "jpn": "ja",
	"kor": "ko", "cat": "ca", "baq": "eu", "eus": "eu", "glg": "gl", "geo": "ka",
	"kat": "ka", "arm": "hy", "hye": "hy", "aze": "az", "kaz": "kk", "uzb": "uz",
	"afr": "af", "swa": "sw", "wel": "cy", "cym": "cy", "gle": "ga", "epo": "eo",
	"lat": "la", "khm": "km", "mon": "mn", "nep": "ne", "sin": "si",
}

// NormalizeLang reduces a language tag to the form the auto-download setting
// uses: lower case, ISO 639-1 where a 639-2 code has one, region kept
// ("pt-br"). Underscores become hyphens. "" for an empty or "und" tag.
func NormalizeLang(tag string) string {
	t := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(tag, "_", "-")))
	if t == "" || t == "und" || t == "unknown" {
		return ""
	}
	base, region, _ := strings.Cut(t, "-")
	if two, ok := iso639_2to1[base]; ok {
		base = two
	}
	if region != "" {
		return base + "-" + region
	}
	return base
}

// langMatches reports whether a subtitle in language have satisfies a wanted
// language: the same tag, or the same base language when either side has no
// region (a wanted "pt" is met by "pt-br"; a wanted "pt-br" by "pt" or
// "pt-br", but not by "pt-pt").
func langMatches(have, want string) bool {
	if have == "" || want == "" {
		return false
	}
	if have == want {
		return true
	}
	hb, hr, _ := strings.Cut(have, "-")
	wb, wr, _ := strings.Cut(want, "-")
	if hb != wb {
		return false
	}
	return wr == "" || hr == ""
}

// ParseLanguageList turns "en, es,pt-BR" into normalised, de-duplicated
// codes, dropping anything that isn't a plain language tag.
func ParseLanguageList(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		code := NormalizeLang(part)
		if code == "" || !validSubtitleLang(code) || seen[code] {
			continue
		}
		seen[code] = true
		out = append(out, code)
	}
	return out
}

// subtitleSidecarExts are the subtitle formats a sidecar file may use.
var subtitleSidecarExts = map[string]bool{
	".srt": true, ".ass": true, ".ssa": true, ".vtt": true, ".sub": true, ".idx": true, ".sup": true,
}

// sidecarLangRe pulls the language tag out of a sidecar name's dotted parts:
// Movie.en.srt, Movie.eng.forced.srt, Movie.pt-BR.sdh.srt.
var sidecarLangRe = regexp.MustCompile(`^[A-Za-z]{2,3}([-_][A-Za-z0-9]{2,4})?$`)

// SidecarLanguages lists the languages of subtitle files sitting next to a
// local video file (Movie.mkv → Movie.en.srt, Movie.eng.forced.ass, …). A
// sidecar with no language part ("Movie.srt") reports "". Unreadable
// directories (object storage, a share that dropped) report nothing.
func SidecarLanguages(videoPath string) []string {
	dir := filepath.Dir(videoPath)
	stem := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if !subtitleSidecarExts[ext] {
			continue
		}
		base := strings.TrimSuffix(name, filepath.Ext(name))
		if !strings.HasPrefix(strings.ToLower(base), strings.ToLower(stem)) {
			continue
		}
		rest := base[len(stem):]
		if rest != "" && rest[0] != '.' {
			// "Movie 2.en.srt" next to "Movie.mkv" belongs to another film.
			continue
		}
		rest = strings.TrimPrefix(rest, ".")
		lang := ""
		for _, part := range strings.Split(rest, ".") {
			if sidecarLangRe.MatchString(part) {
				if n := NormalizeLang(part); n != "" && n != "sdh" && n != "cc" {
					lang = n
					break
				}
			}
		}
		out = append(out, lang)
	}
	return out
}
