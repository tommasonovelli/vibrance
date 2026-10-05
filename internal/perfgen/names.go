//go:build perf

package perfgen

import (
	"math/rand/v2"
	"strconv"
	"strings"
)

// The names of the dataset are made of real words in the scripts a music
// collection has, because what is measured depends on them: the collation
// keys of the lists, and the tokens and the prefixes of the full-text
// index. A few words are very frequent, as "the" and "love" are in a real
// collection: they are the searches that match the most rows.

// language is the words of one language: given names and family names, of
// which the names of its artists are made, and the words of its titles.
type language struct {
	given, family, words []string
	// weight is how many artists in a hundred speak it.
	weight int
}

var languages = []language{
	{ // English
		given: []string{"Miles", "John", "Ella", "Nina", "Aretha", "Joni", "Bob", "Billie", "Marvin", "Stevie", "Kate", "David",
			"Patti", "Leonard", "Nick", "Tom", "Amy", "Ray", "Etta", "Chet", "Thelonious", "Charles", "Sarah", "Otis", "Lou"},
		family: []string{"Davis", "Coltrane", "Fitzgerald", "Simone", "Franklin", "Mitchell", "Dylan", "Holiday", "Gaye", "Wonder",
			"Bush", "Bowie", "Smith", "Cohen", "Cave", "Waits", "Winehouse", "Charles", "James", "Baker", "Monk", "Mingus",
			"Vaughan", "Redding", "Reed", "O'Connor", "McCartney", "Young", "Stone", "Jones"},
		words: []string{"Love", "Blue", "Night", "Heart", "Time", "Road", "River", "Dream", "Fire", "Rain", "Moon", "Sun", "Day",
			"Light", "Dark", "Home", "World", "Song", "Dance", "Train", "City", "Sky", "Girl", "Boy", "Man", "Woman", "Water",
			"Stone", "Gold", "Silver", "Wind", "Summer", "Winter", "Morning", "Midnight", "Soul", "Angel", "Devil", "Heaven",
			"Garden", "Street", "Window", "Mirror", "Shadow", "Echo", "Ghost", "Paper", "Glass", "Velvet", "Electric"},
		weight: 46,
	},
	{ // Italian
		given:  []string{"Lucio", "Fabrizio", "Mina", "Paolo", "Franco", "Giorgio", "Ornella", "Gino", "Luigi", "Francesco"},
		family: []string{"Battisti", "De André", "Mazzini", "Conte", "Battiato", "Gaber", "Vanoni", "Paoli", "Tenco", "Guccini"},
		words: []string{"Amore", "Notte", "Città", "Cuore", "Mare", "Luna", "Sole", "Strada", "Vento", "Tempo", "Casa", "Canzone",
			"Sogno", "Perché", "Così", "Libertà", "Più", "Giù", "Caffè", "Sera"},
		weight: 8,
	},
	{ // French
		given:  []string{"Édith", "Serge", "Jacques", "Françoise", "Georges", "Barbara", "Léo", "Zaz", "Benjamin", "Mylène"},
		family: []string{"Piaf", "Gainsbourg", "Brel", "Hardy", "Brassens", "Ferré", "Biolay", "Farmer", "Aznavour", "Bécaud"},
		words: []string{"Amour", "Nuit", "Cœur", "Été", "Hiver", "Rêve", "Forêt", "Fenêtre", "Où", "Déjà", "Garçon", "Chanson",
			"Lumière", "Ombre", "Étoile", "Mer", "Ciel", "Île", "Noël", "Écho"},
		weight: 7,
	},
	{ // German
		given:  []string{"Nina", "Herbert", "Udo", "Nena", "Klaus", "Marlene", "Jürgen", "Günther", "Björn", "Søren"},
		family: []string{"Hagen", "Grönemeyer", "Lindenberg", "Schulze", "Dietrich", "Müller", "Weiß", "Größe", "Åkesson", "Ødegård"},
		words: []string{"Liebe", "Nacht", "Straße", "Herz", "Träume", "Über", "Mädchen", "Frühling", "Schön", "Größer", "Zeit",
			"Himmel", "Wasser", "Feuer", "Süß", "Fjørd", "Ålesund", "Þögn", "Ljóð", "Ævintýr"},
		weight: 6,
	},
	{ // Spanish and Portuguese
		given:  []string{"Joaquín", "Mercedes", "Víctor", "Chavela", "Caetano", "João", "Gal", "Elis", "Rubén", "Cesária"},
		family: []string{"Sabina", "Sosa", "Jara", "Vargas", "Veloso", "Gilberto", "Costa", "Regina", "Blades", "Évora"},
		words: []string{"Corazón", "Mañana", "Niño", "Canción", "Año", "Señor", "Sueño", "Lágrima", "Saudade", "Coração", "Manhã",
			"Paixão", "Não", "Água", "Noite", "Tierra", "Fuego", "Cielo", "Adiós", "Milonga"},
		weight: 7,
	},
	{ // Polish, Czech, Turkish, Vietnamese
		given:  []string{"Czesław", "Władysław", "Antonín", "Bedřich", "Sezen", "Barış", "Trịnh", "Leoš", "Zbigniew", "Ömer"},
		family: []string{"Niemen", "Szpilman", "Dvořák", "Smetana", "Aksu", "Manço", "Công Sơn", "Janáček", "Preisner", "Çağlar"},
		words: []string{"Miłość", "Żal", "Łąka", "Świat", "Říjen", "Píseň", "Ďábel", "Aşk", "Gözler", "Işık", "Şarkı", "Tình",
			"Nhớ", "Đêm", "Mưa", "Dzień", "Noc", "Ulice", "Rüya", "Yağmur"},
		weight: 4,
	},
	{ // Russian
		given:  []string{"Виктор", "Борис", "Алла", "Владимир", "Земфира", "Булат", "Дмитрий", "Сергей", "Анна", "Пётр"},
		family: []string{"Цой", "Гребенщиков", "Пугачёва", "Высоцкий", "Рамазанова", "Окуджава", "Шостакович", "Рахманинов", "Герман", "Чайковский"},
		words: []string{"Любовь", "Ночь", "Город", "Звезда", "Дорога", "Зима", "Лето", "Сердце", "Песня", "Ветер", "Море", "Небо",
			"Дождь", "Ёлка", "Время", "Группа", "Крови", "Перемен", "Осень", "Весна"},
		weight: 5,
	},
	{ // Greek
		given:  []string{"Μίκης", "Μάνος", "Νάνα", "Μαρία", "Γιώργος", "Χάρις", "Σωκράτης", "Ελευθερία"},
		family: []string{"Θεοδωράκης", "Χατζιδάκις", "Μούσχουρη", "Φαραντούρη", "Νταλάρας", "Αλεξίου", "Μάλαμας", "Αρβανιτάκη"},
		words: []string{"Αγάπη", "Νύχτα", "Θάλασσα", "Ήλιος", "Φεγγάρι", "Δρόμος", "Καρδιά", "Τραγούδι", "Όνειρο", "Άνοιξη",
			"Ουρανός", "Βροχή", "Πόλη", "Χρόνια", "Ζωή", "Ελλάδα"},
		weight: 2,
	},
	{ // Japanese
		given:  []string{"坂本", "久石", "宇多田", "椎名", "細野", "山下", "中島", "矢野", "大貫", "竹内"},
		family: []string{"龍一", "譲", "ヒカル", "林檎", "晴臣", "達郎", "みゆき", "顕子", "妙子", "まりや"},
		words: []string{"東京", "夜", "愛", "夢", "風", "花", "雨", "空", "海", "月", "星", "桜", "さよなら", "ありがとう", "ラブ",
			"ストーリー", "サマー", "ブルー", "こころ", "ひかり", "物語", "旅立ち", "プラスティック", "ガール"},
		weight: 5,
	},
	{ // Chinese
		given:  []string{"王", "周", "鄧", "張", "陳", "李", "林", "蔡", "梅", "羅"},
		family: []string{"菲", "杰倫", "麗君", "學友", "奕迅", "宗盛", "憶蓮", "琴", "艷芳", "大佑"},
		words: []string{"月亮", "代表", "我的心", "愛情", "故事", "夜", "上海", "紅豆", "天空", "朋友", "時間", "夢", "青春",
			"海闊天空", "光輝歲月", "甜蜜蜜", "小城", "但願", "人長久", "千千闕歌"},
		weight: 3,
	},
	{ // Korean
		given:  []string{"김", "이", "박", "조", "신", "유", "서", "장"},
		family: []string{"광석", "소라", "효신", "용필", "중현", "재하", "태지", "필순"},
		words: []string{"사랑", "밤", "서울", "바람", "꿈", "하늘", "바다", "비", "눈", "별", "노래", "이야기", "그대", "봄날",
			"가을", "편지", "기억", "시간"},
		weight: 2,
	},
	{ // Arabic and Hebrew
		given:  []string{"أم", "فيروز", "عبد الحليم", "محمد", "وردة", "أسمهان", "עפרה", "אריק", "שלמה", "חוה"},
		family: []string{"كلثوم", "الرحباني", "حافظ", "عبد الوهاب", "الجزائرية", "الأطرش", "חזה", "איינשטיין", "ארצי", "אלברשטיין"},
		words: []string{"حب", "ليلة", "قلب", "القمر", "البحر", "عمر", "أغنية", "حياتي", "الأطلال", "بيروت", "אהבה", "לילה",
			"ירושלים", "שיר", "ים", "אור", "שלום", "חלום"},
		weight: 3,
	},
	{ // Hindi and Thai
		given:  []string{"लता", "किशोर", "आशा", "मोहम्मद", "रवि", "ธงไชย", "อัสนี", "พงษ์สิทธิ์"},
		family: []string{"मंगेशकर", "कुमार", "भोसले", "रफ़ी", "शंकर", "แมคอินไตย์", "โชติกุล", "คำภีร์"},
		words: []string{"प्यार", "दिल", "रात", "ज़िंदगी", "सपना", "चाँद", "बारिश", "गीत", "कहानी", "दोस्ती", "รัก", "คืน", "ฝัน",
			"ดาว", "ทะเล", "เพลง", "หัวใจ", "ฝน"},
		weight: 2,
	},
}

// english is what every language borrows a title from now and then.
var english = &languages[0]

// The words a real collection repeats most.
var (
	adjectives = []string{"Blue", "Red", "Black", "White", "Golden", "Lonely", "Little", "Sweet", "Wild", "Broken", "Last",
		"First", "New", "Old", "Strange", "Electric", "Silent", "Crazy", "Good", "Bad"}
	bandWords = []string{"Beatles", "Stones", "Doors", "Kinks", "Clash", "Cure", "Smiths", "Pixies", "Ramones", "Supremes",
		"Temptations", "Wailers", "Shadows", "Animals", "Birds", "Machines", "Brothers", "Sisters", "Orchestra", "Quartet",
		"Trio", "Ensemble", "Collective", "Experience", "Project", "Band"}
	genres = []string{"Rock", "Jazz", "Pop", "Classical", "Electronic", "Hip-Hop", "Folk", "Blues", "Soul", "R&B", "Reggae",
		"Country", "Metal", "Punk", "Ambient", "Soundtrack", "World", "Latin", "Chanson", "Cantautori", "J-Pop", "Bossa Nova",
		"Post-Punk", "Krautrock", "Musique concrète"}
	movements = []string{"Allegro", "Adagio", "Andante", "Presto", "Largo", "Scherzo", "Menuetto", "Rondo", "Allegro ma non troppo",
		"Molto vivace"}
	keys = []string{"C major", "D minor", "E-flat major", "F-sharp minor", "G major", "A minor", "B-flat major", "C-sharp minor"}
)

// pick returns one of the values.
func pick(rng *rand.Rand, values []string) string {
	return values[rng.IntN(len(values))]
}

// speak chooses the language of an artist, by the weights.
func speak(rng *rand.Rand) *language {
	n := rng.IntN(100)
	for i := range languages {
		if n -= languages[i].weight; n < 0 {
			return &languages[i]
		}
	}
	return english
}

// artistName makes the name of an artist in a language: a person, or a
// band. Two calls may give one name: the caller tries again.
func artistName(rng *rand.Rand, l *language) string {
	switch n := rng.IntN(10); {
	case l == english && n < 2:
		return "The " + pick(rng, bandWords)
	case l == english && n < 4:
		return "The " + pick(rng, adjectives) + " " + pick(rng, bandWords)
	case n < 2:
		return pick(rng, l.words) + " " + pick(rng, bandWords)
	case n < 3:
		return pick(rng, l.given) + " " + pick(rng, l.family) + " " + pick(rng, bandWords)
	case n < 4:
		return pick(rng, l.given) + " " + pick(rng, l.family) + " & " + pick(rng, l.given) + " " + pick(rng, l.family)
	}
	return pick(rng, l.given) + " " + pick(rng, l.family)
}

// title makes the title of an album or of a track of an artist that speaks
// l. One title in five is in English whatever the language, and the
// commonest words of a collection ("the", "of", "love", "you") come back
// often.
func title(rng *rand.Rand, l *language) string {
	if rng.IntN(5) == 0 {
		l = english
	}
	w := func() string { return pick(rng, l.words) }
	switch n := rng.IntN(20); {
	case n < 4:
		return w()
	case n < 8:
		return w() + " " + w()
	case n < 10 && l == english:
		return "The " + pick(rng, adjectives) + " " + w()
	case n < 12 && l == english:
		return w() + " of the " + w()
	case n < 13 && l == english:
		return "I Love You, " + w()
	case n < 15 && l == english:
		return pick(rng, adjectives) + " " + w() + " in the " + w()
	case n < 16:
		return w() + " " + w() + " " + w()
	case n < 17:
		return w() + ", Pt. " + strconv.Itoa(1+rng.IntN(4))
	case n < 18:
		return w() + " (Live)"
	case n < 19:
		return w() + ": " + w() + "?"
	}
	return w() + " " + strconv.Itoa(1950+rng.IntN(75))
}

// movement is the title of a track of a classical album.
func movement(rng *rand.Rand, work, no int) string {
	return "Symphony No. " + strconv.Itoa(work) + " in " + pick(rng, keys) + ": " + roman(no) + ". " + pick(rng, movements)
}

func roman(n int) string {
	numerals := []string{"I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X"}
	if n >= 1 && n <= len(numerals) {
		return numerals[n-1]
	}
	return strconv.Itoa(n)
}

// folder is a name as MusicLib writes it on disk (DESIGN.md §4.1): the
// characters a file system refuses become "_". The path loses information,
// and nothing reads a name from it.
func folder(name string) string {
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, name)
	return strings.TrimRight(name, ". ")
}
