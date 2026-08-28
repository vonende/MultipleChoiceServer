package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Struktur für die eingehenden POST-Daten (JSON)
type LoginRequest struct {
	Name string `json:"name"`
}

// Frage repräsentiert eine einzelne Frage im Test, einschließlich Typ, Text, Bild, Optionen und Punktwerte.
type Frage struct {
	ID         int
	Typ        string // "SINGLE", "MULTI" oder "TEXT"
	Text       string
	Bild       string         // Dateiname, z.B. "bild.jpg"
	Optionen   []string       // Nur für SINGLE und MULTI relevant
	Punktwerte map[string]int // Nur für MULTI relevant
	Punkte     int            // Gesamtpunktzahl für die Frage
}

// TestDaten enthält die Testeinstellungen und die Liste der Fragen.
type TestDaten struct {
	Titel            string
	TestDauerMinuten int
	Ergebnisdatei    string
	Fragen           []Frage
	Zurueck          bool
	AnzSchueler      int
	Schueler         []string
}

// testdaten ist eine globale Variable, die die Testeinstellungen und Fragen enthält.
// Sie wird beim Start des Programms mit Standardwerten initialisiert und später
// mit den Werten aus der Fragendatei überschrieben.
var testdaten = &TestDaten{
	Titel:            "Test",
	TestDauerMinuten: 10,
	Ergebnisdatei:    "Ergebnisse",
	Zurueck:          false,
	AnzSchueler:      0,
}

// templates ist ein globales Template-Objekt, das die HTML-Vorlage für die Webseite enthält.
var templates = template.Must(template.ParseFiles("template.html"))

// main startet den HTTP-Server, lädt die Fragen und Einstellungen und wartet auf eingehende Anfragen.
func main() {
	var err error

	// Logfile öffnen oder erstellen (O_APPEND fügt neue Logs unten an)
	datei, err := os.OpenFile("mcs.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Fehler beim Öffnen der Logdatei: %v", err)
	}
	defer datei.Close()

	// MultiWriter erstellen: Schreibt in die Datei UND in das Terminal (os.Stdout)
	multiWriter := io.MultiWriter(os.Stdout, datei)
	log.SetOutput(multiWriter)

	log.Print("Server wird gestartet...\n")

	server := &http.Server{Addr: ":8080"}

	// Kommandozeilenparameter für die Fragendatei und dieErgebnisdatei einlesen
	// Es kann hinter dem Programmaufruf Dateinamen angegeben werden, z.B.:
	// go run main.go Testdatei Ergebnisse
	// oder
	// go run main.go -in=Testdatei -out=Ergebnisse
	// Wenn kein Dateiname angegeben wird, wird der Wert aus den Einstellungen oder ein Standardwert verwendet.
	inputFilePtr := flag.String("in", "Fragen", "Dateiname für die Fragen")
	resultFilePtr := flag.String("out", "Ergebnisse", "Dateiname für die Ergebnisdatei")
	flag.Parse()
	if flag.NArg() > 0 {
		*inputFilePtr = flag.Arg(0)
	}
	if !strings.HasSuffix(strings.ToLower(*inputFilePtr), ".txt") {
		*inputFilePtr += ".txt"
	}
	if flag.NArg() > 1 {
		*resultFilePtr = flag.Arg(1)
	}
	testdaten.Ergebnisdatei = *resultFilePtr

	// Fragen und Einstellungen laden
	testdaten.Fragen, err = ladeFragen(*inputFilePtr)
	if err != nil {
		log.Fatalf("Kritischer Fehler beim Laden der Fragen: %v", err)
	}
	log.Printf("Es wurden %d Fragen geladen.\n", len(testdaten.Fragen))

	// Bilder bereitstellen
	// Alles unter URL /bilder/ wird aus dem lokalen Ordner "./bilder" geholt
	fs := http.FileServer(http.Dir("./bilder"))
	http.Handle("/bilder/", http.StripPrefix("/bilder/", fs))

	// Routen definieren
	http.HandleFunc("/", handleLogin)
	http.HandleFunc("/start-test", handleStartTest)
	http.HandleFunc("/submit", handleSubmit)

	// Server in einem separaten Goroutine starten, um auf eingehende Anfragen zu warten
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server-Fehler: %s\n", err)
		}
	}()
	log.Printf("Server läuft auf %s:8080\n", getIP())

	// Maximale Laufzeit des Servers auf 2 Stunden setzen, danach wird der Server automatisch beendet.
	maxRuntime := 2 * time.Hour
	runCtx, runCancel := context.WithTimeout(context.Background(), maxRuntime)
	defer runCancel()

	// Kanal für das Abfangen von Signalen (z.B. Strg+C)
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	// Auf das Signal (z.B. Strg+C) warten
	select {
	case <-runCtx.Done():
		log.Println("Maximale Laufzeit erreicht. Server wird beendet...")
	case sig := <-stopChan:
		log.Printf("Signal \"%v\" empfangen. Server fährt herunter...\n", sig)
	}

	// Timeout von 4 Sekunden für das Herunterfahren des Servers
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer shutdownCancel()

	// Server sauber herunterfahren
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server-Shutdown fehlgeschlagen: %s\n", err)
	}

	log.Println("Server erfolgreich heruntergefahren.")
}

// handleLogin rendert die Login-Seite, auf der der Schüler seinen Namen eingeben kann.
func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	data := struct {
		Titel string
	}{testdaten.Titel}

	templates.ExecuteTemplate(w, "template.html", data)
}

// handleStartTest verarbeitet die eingehenden POST-Daten (Schülername) und generiert die Fragen in zufälliger Reihenfolge.
func handleStartTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// JSON-Daten aus dem Request-Body auslesen
	var req LoginRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil || req.Name == "" {
		http.Error(w, "Ungültige Daten", http.StatusBadRequest)
		return
	}

	testdaten.AnzSchueler++
	testdaten.Schueler = append(testdaten.Schueler, req.Name)
	log.Printf("%s hat mit dem Test begonnen. Es sind %d Schüler aktiv.", req.Name, testdaten.AnzSchueler)

	// Zufällige Reihenfolge der Fragen generieren
	// z ist eine Permutation der Indizes der Fragen und wird in einem sclice von int gespeichert.
	z := make([]int, len(testdaten.Fragen))
	for i := 0; i < len(testdaten.Fragen); i++ {
		z[i] = i
	}
	rand.Shuffle(len(z), func(i, j int) {
		z[i], z[j] = z[j], z[i]
	})

	// Wir rendern NUR das HTML-Fragment für die Fragen und senden es zurück
	w.Header().Set("Content-Type", "text/html")

	// Der Wert des Timers wird in einem versteckten Input-Feld gespeichert, damit das JavaScript darauf zugreifen kann.
	fmt.Fprintf(w, `<input type="hidden" id="timer-dauer" value="%d">`, testdaten.TestDauerMinuten)
	fmt.Fprintf(w, `<input type="hidden" id="zurueck" value="%t">`, testdaten.Zurueck)

	fmt.Fprintf(w, "<p>Name: <strong>%s</strong></p>", template.HTMLEscapeString(req.Name))

	aktiv := " aktiv"
	for i := 0; i < len(testdaten.Fragen); i++ {
		frage := testdaten.Fragen[z[i]]
		fmt.Fprintf(w, `
			<div class="frage-box%s">
				<p><strong>Frage %d</strong> (%d Punkte)</p>
				<p style="font-size: 1.1em;">%s</p>
		`, aktiv, i+1, frage.Punkte, HTMLLinebreak(template.HTMLEscapeString(frage.Text)))
		aktiv = "" // Nur die erste Frage ist aktiv, die anderen sind standardmäßig ausgeblendet
		if frage.Bild != "" {
			fmt.Fprintf(w, `<div class="img-container"><img src="/bilder/%s" alt="Bild zur Frage"></div>`, template.HTMLEscapeString(frage.Bild))
		}
		switch frage.Typ {
		case "SINGLE":
			for optIndex, opt := range frage.Optionen {
				fmt.Fprintf(w, `<label><input type="radio" name="frage_%d" value="%s"> %s</label><br>`, frage.ID, string(rune('A'+optIndex)), template.HTMLEscapeString(opt))
			}
		case "MULTI":
			for optIndex, opt := range frage.Optionen {
				fmt.Fprintf(w, `<label><input type="checkbox" name="frage_%d" value="%s"> %s</label><br>`, frage.ID, string(rune('A'+optIndex)), template.HTMLEscapeString(opt))
			}
		case "TEXT":
			fmt.Fprintf(w, `<label>Deine Antwort:</label><textarea name="frage_%d" placeholder="Schreibe hier deine Antwort..."></textarea>`, frage.ID)
		default:
			fmt.Fprintf(w, `<p style="color: red;">Unbekannter Fragetyp: %s</p>`, template.HTMLEscapeString(frage.Typ))
		}
		if i == len(testdaten.Fragen)-1 {
			fmt.Fprintf(w, `<p style="margin: auto; color: blue; text-align: center;">*** Das ist die letzte Frage. ***</p>`)
		}
		fmt.Fprintf(w, `</div>`)
	}
}

// handleSubmit verarbeitet die eingehenden Antworten des Schülers, bewertet die Multiple-Choice-
// und Multi-Antwort-Fragen automatisch und speichert die Ergebnisse in einer Text- und CSV-Datei.
func handleSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	r.ParseForm()
	name := r.FormValue("schuelerName")

	autoPunkte := 0
	maxAutoPunkte := 0
	IP := getClientIP(r)

	// String Builder für den Bericht
	var bericht strings.Builder
	var csv strings.Builder

	csv.WriteString(fmt.Sprintf("%s; %s; %s;", name, IP, time.Now().Format("2006-01-02 15:04")))

	bericht.WriteString("\n\n================================================================================\n")
	bericht.WriteString(fmt.Sprintf("NAME: %s\nDATUM: %s\nIP: %s\n", name, time.Now().Format("2006-01-02 15:04"), IP))

	for i, frage := range testdaten.Fragen {
		bericht.WriteString("--------------------------------------------------------------------------------\n")

		antwortKey := fmt.Sprintf("frage_%d", i)
		gegebeneAntworten := r.Form[antwortKey]
		gegebeneAntwort := r.FormValue(antwortKey)

		switch frage.Typ {
		case "SINGLE":
			punkte := bewerteAntworten(frage, []string{gegebeneAntwort})
			csv.WriteString(fmt.Sprintf("%d;", punkte))
			maxAutoPunkte += frage.Punkte
			autoPunkte += punkte
			bericht.WriteString(fmt.Sprintf("Frage %d (SINGLE): %d / %d Pkt\n", i+1, punkte, frage.Punkte))
		case "MULTI":
			punkte := bewerteAntworten(frage, gegebeneAntworten)
			csv.WriteString(fmt.Sprintf("%d;", punkte))
			maxAutoPunkte += frage.Punkte
			autoPunkte += punkte
			bericht.WriteString(fmt.Sprintf("Frage %d (MULTI): %d / %d Pkt\n", i+1, punkte, frage.Punkte))
		default:
			// Bei Textfragen speichern wir den Text zur manuellen Bewertung
			csv.WriteString(";") // Textfragen werden nicht automatisch bewertet
			bericht.WriteString(fmt.Sprintf("Frage %d (TEXT, max %d Pkt):\n", i+1, frage.Punkte))
			bericht.WriteString(fmt.Sprintf(">> ANTWORT: \n%s\n", textMaxWidth(gegebeneAntwort, 80)))
		}
	}
	csv.WriteString("\n") // Zeilenumbruch am Ende der Zeile im CSV
	bericht.WriteString("================================================================================\n")
	bericht.WriteString(fmt.Sprintf("AUTO-WERTUNG: %d / %d Punkte\n", autoPunkte, maxAutoPunkte))
	bericht.WriteString("================================================================================\n")

	gefunden := false
	for i, s := range testdaten.Schueler {
		if s == name {
			// Speichern
			saveResult(bericht.String(), csv.String())

			// Feedback an Schüler
			fmt.Fprint(w, `
						<!DOCTYPE html>
						<html lang="de">
						<head>
							<meta charset="UTF-8">
							<meta name="viewport" content="width=device-width, initial-scale=1.0">
						</head>
						<body>
							<p>Deine Antworten wurden gespeichert.</p>`)
			fmt.Fprintf(w, "<p>Deine automatisierte Auswertung der Multiple-Choice-Fragen: <strong>%d / %d</strong> Punkten.</p>", autoPunkte, maxAutoPunkte)
			fmt.Fprint(w, `
							<br><a href="/">Zum Start</a>
						</body>
					`)

			testdaten.AnzSchueler--
			log.Printf("%s hat den Test abgegeben. Es sind noch %d Schüler aktiv.\n", name, testdaten.AnzSchueler)
			testdaten.Schueler = slices.Delete(testdaten.Schueler, i, i+1)
			gefunden = true
			break
		}
	}
	if !gefunden {
		fmt.Fprint(w, `
					<!DOCTYPE html>
					<html lang="de">
					<head>
						<meta charset="UTF-8">
						<meta name="viewport" content="width=device-width, initial-scale=1.0">
					</head>
					<body>
						<p style="color: red"><strong>Du musst dich erst anmelden.</strong></p>
						<br><a href="/">Zum Start</a>
					</body>
					`)
		log.Printf("%s hat versucht abzugeben, obwohl er nicht angemeldet ist.", name)
		log.Print(bericht.String())
	}
}

// ladeFragen liest die Fragen aus der angegebenen Datei ein und gibt sie als Slice von Frage-Strukturen zurück.
// Die Datei sollte im Format "Typ|Fragetext|Bild|Optionen|Punktwerte" vorliegen, wobei Typ "SINGLE", "MULTI" oder "TEXT" sein kann.
// Kommentare in der Datei beginnen mit "#" und werden ignoriert. Testeinstellungen können mit "!" angegeben werden.
// Die Punktwerte für MULTI-Fragen werden im Format "A=1;B=0;C=-1" angegeben.
// Die Funktion gibt einen Fehler zurück, wenn die Datei nicht gelesen werden kann oder das Format ungültig ist.
// Die Funktion speichert auch die Punktzahlen in einer CSV-Datei, die den Namen der Ergebnisdatei hat.
func ladeFragen(filename string) ([]Frage, error) {
	data, err := os.ReadFile(filterDateiname(filename))
	if err != nil {
		return nil, err
	}

	lines := prepareLines(string(data))

	var result []Frage
	var csv strings.Builder
	csv.WriteString(fmt.Sprintf("Aaallerbeste; %s; %s;", getIP(), time.Now().Format("2006-01-02 15:04"))) // Headerzeile für CSV-Datei

	var fragenCounter int = 0

	for i := 0; i < len(lines); i++ {

		parts := splitLine(lines[i])

		if strings.HasPrefix(parts[0], "!") {

			if len(parts) < 3 {
				log.Printf("Zeile %d übersprungen: Zu wenige Parameter bei den Testeinstellungen.", i+1)
				continue
			}

			// Titel
			if parts[1] == "" {
				log.Printf("Ungültiger Titel in Zeile %d.", i+1)
			} else {
				testdaten.Titel = parts[1]
			}

			var dauer int
			dauer, err = strconv.Atoi(parts[2])
			if err != nil || dauer <= 0 {
				log.Printf("Ungültige Testdauer in Zeile %d.", i+1)
			} else {
				testdaten.TestDauerMinuten = dauer
			}

			// Falls die Spalte für den Parameter "zurück" fehlt,
			// wird die Zeile übersprungen. Es wird dann kein
			// Zurückbutton angezeigt.
			if len(parts) < 4 {
				continue
			}
			z := strings.ToLower(strings.TrimSpace(parts[3]))
			if z == "zurück" || z == "zurueck" {
				testdaten.Zurueck = true
			}

			continue
		}

		if len(parts) < 4 {
			log.Printf("Zeile %d übersprungen: Zu wenig Spalten\n%s", i+1, lines[i])
			for _, p := range parts {
				log.Println(p)
			}
			continue
		}

		typ := strings.ToUpper(parts[0])
		if len(parts) < 2 {
			log.Printf("Zeile %d übersprungen: Ungültiges Format. %s", i+1, lines[i])
			continue
		}

		p := len(parts)
		pkt := 0
		pktValue := parts[p-1]

		frage := Frage{
			Typ:    typ,
			Text:   parts[1],
			Bild:   parts[2],
			Punkte: pkt,
		}

		if p >= 4 {
			frage.Optionen = append([]string{}, parts[3:p-1]...)
		}

		switch {
		case typ == "SINGLE":
			frage.Punktwerte = parsePunktwerte(pktValue)
			for _, value := range frage.Punktwerte {
				pkt = max(pkt, value)
			}
			frage.Punkte = pkt
			csv.WriteString(fmt.Sprintf("%d;", pkt))
		case typ == "MULTI":
			frage.Punktwerte = parsePunktwerte(pktValue)
			for str, value := range frage.Punktwerte {
				if value > 0 {
					pkt += value
				}
				if str == "MAX" {
					pkt = value
					break
				}
			}
			frage.Punkte = pkt
			csv.WriteString(fmt.Sprintf("%d;", pkt))
		case typ == "TEXT":
			pkt, _ = strconv.Atoi(pktValue)
			frage.Punkte = pkt
			csv.WriteString(fmt.Sprintf("%d;", pkt))
		default:
			log.Printf("Zeile %d übersprungen: Unbekannter Fragetyp '%s'", i+1, typ)
			continue
		}
		frage.ID = fragenCounter
		fragenCounter++
		result = append(result, frage)
	}
	csv.WriteString("\n")        // Zeilenumbruch am Ende der Zeile im CSV
	saveResult("", csv.String()) // Speichern der CSV-Datei mit den Punktzahlen
	return result, nil
}

// filterDateiname entfernt alle ungültigen Zeichen aus dem Dateinamen,
// um sicherzustellen, dass der Dateiname gültig ist.
func filterDateiname(dateiname string) string {
	// Erstelle Regex: Suche nach allen Zeichen außer Buchstaben, Zahlen, Bindestrich und Punkt
	reg := regexp.MustCompile(`[^a-zA-Z0-9.\-_\\\/äÄöÖüÜß\(\) ]`)
	// Löscht alle ungültigen Zeichen
	return reg.ReplaceAllString(dateiname, "")
}

// enthaeltString prüft, ob das Stringarray den Suchstring enthält. Groß-/Kleinschreibung wird ignoriert und führende/trailing Whitespaces werden entfernt.
func enthaeltString(suche *string, s *[]string) bool {
	for _, str := range *s {
		if strings.Compare(*suche, strings.TrimSpace(strings.ToUpper(str))) == 0 {
			return true
		}
	}
	return false
}

// bewerteAntworten bewertet die Antworten auf eine MULTI-Frage und gibt die Gesamtpunktzahl zurück.
func bewerteAntworten(frage Frage, antworten []string) int {
	punkte := 0
	if maxPunkte, ok := frage.Punktwerte["MAX"]; ok {
		punkte = maxPunkte
		for i := 0; i < len(frage.Punktwerte)-1; i++ {
			nr := string(rune('A' + i))
			if wert, ok := frage.Punktwerte[nr]; ok {
				if wert > 0 {
					if !enthaeltString(&nr, &antworten) {
						punkte--
					}
				} else {
					if enthaeltString(&nr, &antworten) {
						punkte--
					}
				}
			}
		}
	} else {
		for _, antwort := range antworten {
			antwort = strings.TrimSpace(strings.ToUpper(antwort))
			if wert, ok := frage.Punktwerte[antwort]; ok {
				punkte += wert
			}
		}
	}
	if punkte < 0 {
		punkte = 0
	}
	return punkte
}

// parsePunktwerte parst eine Zeichenkette mit Punktwerten im Format "A=1 B=0 C=-1"
// und gibt eine Map zurück, die die Buchstaben den entsprechenden Punktwerten zuordnet.
func parsePunktwerte(str string) map[string]int {
	result := make(map[string]int)
	if str == "" {
		return result
	}

	r := regexp.MustCompile(`[A-Za-z]+ *\t*= *\t*-*\d+`)
	strs := r.FindAllString(str, -1)
	if strs == nil {
		return result
	}

	for _, s := range strs {
		parts := strings.SplitN(s, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(strings.ToUpper(parts[0]))
		wert, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			continue
		}
		result[key] = wert
	}
	//log.Println(result)
	return result
}

// saveResult speichert den Bericht und die CSV-Daten in den entsprechenden Dateien.
func saveResult(bericht string, csv string) {
	if bericht != "" {
		f, err := os.OpenFile(filterDateiname(testdaten.Ergebnisdatei+".txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Println("Fehler beim Speichern des Berichts:", err)
			return
		}
		defer f.Close()
		if _, err := f.WriteString(bericht); err != nil {
			log.Println("Fehler beim Schreiben des Berichts:", err)
		}
	}

	if csv != "" {
		f2, err := os.OpenFile(filterDateiname(testdaten.Ergebnisdatei+".csv"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Println("Fehler beim Speichern des CSV:", err)
			return
		}
		defer f2.Close()
		if _, err := f2.WriteString(csv); err != nil {
			log.Println("Fehler beim Schreiben des CSV:", err)
		}
	}
}

// getIP ermittelt die lokale IP-Adresse des Servers, indem es eine Verbindung zu einem externen Server (Google DNS) herstellt und die lokale Adresse abruft.
func getIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

// getClientIP extrahiert die IP-Adresse des Clients aus der HTTP-Anfrage.
func getClientIP(r *http.Request) string {
	// SplitHostPort trennt die IP vom Port (z.B. "192.168.1.50:54321" -> "192.168.1.50")
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr // Fallback, falls kein Port enthalten ist
	}
	return ip
}

// textMaxWidth bricht einen langen Text in mehrere Zeilen um, sodass keine Zeile länger als maxWidth Zeichen ist.
func textMaxWidth(s string, maxWidth int) string {
	if len(s) <= maxWidth {
		return s
	}
	fields := strings.Fields(s)
	var result strings.Builder
	currentLineLength := 0
	for _, field := range fields {
		if currentLineLength+len(field) < maxWidth {
			result.WriteString(field)
			result.WriteString(" ")
			currentLineLength += len(field) + 1
		} else {
			result.WriteString("\n")
			result.WriteString(field)
			result.WriteString(" ")
			currentLineLength = len(field) + 1
		}
	}
	return strings.TrimSpace(result.String())
}

// Sucht in dem String nach doppelten Zeilenumbrüchen,
// und splittet den String an diesen.
// In den Substrings werden anschließend führender und hintenstehender
// Whitespace entfernt.
// Die Substrings werden als sclice zurückgegeben.
func prepareLines(str string) []string {
	// Alle Kommentarzeilen entfernen
	var isComment bool = false
	var backslash bool = false
	var s strings.Builder
	for _, c := range str {
		switch c {
		case '\\':
			if isComment {
				continue
			}
			backslash = true
			s.WriteRune(c)
		case '#':
			if isComment {
				continue
			}
			if !backslash {
				isComment = true
			} else {
				s.WriteRune(c)
			}
			backslash = false
		case '\n':
			if isComment {
				isComment = false
			} else {
				s.WriteRune(c)
			}
			backslash = false
		default:
			if isComment {
				continue
			} else {
				s.WriteRune(c)
			}
			backslash = false
		}

	}
	str = s.String()

	// Wiederholte Zeilenumbrüche erkennen und Whitespaces entfernen,
	// damit die einzelnen Fragen geparst werden können.
	r := regexp.MustCompile(`\n *\t*\r*\n+`)
	str = r.ReplaceAllString(str, "\n\n")

	// Daten zu Fragen in einzelne Strings trennen
	strs := strings.Split(str, "\n\n")

	for i, _ := range strs {
		strs[i] = strings.TrimSpace(strs[i])
	}
	return strs
}

// An einem Semikolon wird gesplittet, falls ihm kein
// Backslash vorangestellt ist.
func splitLine(str string) []string {
	var strs []string
	var s strings.Builder
	var backslash bool = false
	var trimedStr string
	for _, r := range str {
		switch r {
		case '\\':
			if backslash {
				backslash = false
				s.WriteRune(r)
			} else {
				backslash = true
			}
		case ';':
			if backslash {
				s.WriteRune(r)
				backslash = false
			} else {
				trimedStr = strings.TrimSpace(s.String())
				if !strings.HasPrefix(trimedStr, "#") {
					strs = append(strs, trimedStr)
				}
				s.Reset()
			}
		default:
			s.WriteRune(r)
		}
	}
	trimedStr = strings.TrimSpace(s.String())
	if !strings.HasPrefix(trimedStr, "#") {
		strs = append(strs, trimedStr)
	}
	return strs
}

func HTMLLinebreak(str string) string {
	var s strings.Builder
	for _, r := range str {
		if r == '\n' {
			s.WriteString("<br>")
		} else {
			s.WriteRune(r)
		}
	}
	return s.String()
}
