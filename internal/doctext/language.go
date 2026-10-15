package doctext

import (
	"slices"
	"strings"
	"unicode"
)

// ocrLanguages maps OCR language codes (the languages hint of a file, as
// Tesseract names them) to Snowball language names.
var ocrLanguages = map[string]string{
	"ara": "arabic", "dan": "danish", "nld": "dutch", "eng": "english", "fin": "finnish", "fra": "french",
	"deu": "german", "hun": "hungarian", "gle": "irish", "ita": "italian", "nor": "norwegian", "por": "portuguese",
	"ron": "romanian", "rus": "russian", "spa": "spanish", "swe": "swedish", "tam": "tamil", "tur": "turkish",
}

// stopwords are frequent words of each language the detector knows; the
// language with the most of them in a text is its language.
var stopwords = map[string][]string{
	"english":    {"the", "and", "of", "to", "in", "is", "that", "for", "with", "this", "are", "was", "be", "on", "it", "you", "not", "have", "from", "by"},
	"german":     {"der", "die", "und", "das", "ist", "nicht", "mit", "den", "von", "zu", "ein", "eine", "auf", "für", "sich", "dem", "des", "auch", "wir", "sie"},
	"french":     {"le", "la", "les", "et", "des", "est", "une", "un", "pour", "dans", "que", "qui", "pas", "sur", "du", "au", "avec", "nous", "vous", "ce"},
	"spanish":    {"el", "la", "los", "las", "y", "es", "que", "del", "por", "con", "una", "para", "se", "su", "lo", "como", "pero", "al", "más", "está"},
	"italian":    {"il", "la", "che", "di", "e", "non", "per", "una", "sono", "del", "della", "con", "gli", "si", "le", "da", "anche", "come", "questo", "è"},
	"portuguese": {"o", "os", "que", "não", "uma", "com", "para", "do", "da", "dos", "em", "por", "se", "na", "no", "mais", "como", "ao", "ou", "é"},
	"dutch":      {"de", "het", "een", "en", "van", "is", "niet", "dat", "op", "te", "zijn", "met", "voor", "ik", "aan", "ook", "maar", "wij", "hij", "deze"},
	"swedish":    {"och", "att", "det", "som", "en", "är", "för", "på", "med", "inte", "har", "av", "till", "den", "jag", "om", "ett", "var", "vi", "men"},
	"danish":     {"og", "at", "det", "er", "en", "til", "på", "som", "med", "har", "ikke", "af", "for", "den", "jeg", "vi", "et", "var", "kan", "de"},
	"norwegian":  {"og", "det", "er", "som", "en", "til", "på", "med", "ikke", "har", "av", "for", "den", "jeg", "vi", "et", "var", "kan", "de", "ble"},
	"finnish":    {"ja", "on", "ei", "se", "että", "oli", "kun", "hän", "mutta", "ovat", "tai", "myös", "kuin", "niin", "sen", "ole", "joka", "mitä", "vain", "jos"},
	"hungarian":  {"a", "az", "és", "hogy", "nem", "is", "egy", "van", "meg", "de", "már", "csak", "mint", "volt", "ez", "kell", "még", "vagy", "lesz", "pedig"},
	"romanian":   {"și", "în", "de", "la", "cu", "nu", "este", "că", "pe", "un", "o", "care", "pentru", "mai", "din", "sunt", "se", "fost", "dar", "acest"},
	"russian":    {"и", "в", "не", "на", "что", "с", "по", "это", "как", "из", "к", "для", "от", "он", "но", "так", "его", "мы", "все", "был"},
	"turkish":    {"ve", "bir", "bu", "da", "de", "için", "ile", "çok", "daha", "ne", "gibi", "olarak", "ama", "kadar", "en", "var", "olan", "değil", "mi", "sonra"},
}

// Language names a text's language: the file's languages hint when it names
// one Snowball language, else the detected language among the hinted ones
// (or among languages), else empty.
func Language(text, hints string, languages []string) string {
	var hinted []string
	for _, code := range strings.Split(hints, "+") {
		if name := ocrLanguages[strings.TrimSpace(code)]; name != "" && !slices.Contains(hinted, name) {
			hinted = append(hinted, name)
		}
	}
	if len(hinted) == 1 {
		return hinted[0]
	}
	candidates := hinted
	if len(candidates) == 0 {
		candidates = languages
	}
	return detect(text, candidates)
}

// detect returns the candidate whose stopwords occur most often in text,
// when the text is long enough and one language clearly leads.
func detect(text string, candidates []string) string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) })
	if len(words) < 8 || len(candidates) == 0 {
		return ""
	}
	if len(words) > 2000 {
		words = words[:2000]
	}
	counts := map[string]int{}
	for _, w := range words {
		counts[w]++
	}
	best, second, winner := 0, 0, ""
	for _, lang := range candidates {
		score := 0
		for _, s := range stopwords[lang] {
			score += counts[s]
		}
		switch {
		case score > best:
			best, second, winner = score, best, lang
		case score > second:
			second = score
		}
	}
	// Near languages share words: the leader must stand out.
	if best < 3 || best*4 < second*5 {
		return ""
	}
	return winner
}
