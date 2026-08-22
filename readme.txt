-----------------------------------------------------------------
                  mcs - multiple choice server
-----------------------------------------------------------------

Die Software ist ein HTML-Server für einfache Onlinetests.

Fragendatei
-----------
  Die Fragen für den Test müssen in einer Textdatei vorliegen, 
  welche die Endung ".txt" haben muss. Die Endung muss aber nicht
  explizit angegeben werden. Die Struktur der Textdatei wird
  in der Datei "Beispielfragen.txt" exemplarisch erklärt. 

Ergebnisdatei
-------------
  Die Ergebnisse werden in zwei Dateien ausgegeben. Einmal in 
  einer einfach lesbaren Textdatei und in einer CSV-Datei.
  Die erste Zeile der CSV-Datei enthält zu einem fiktiven
  Schülernamen "Aaallerbester" die Maximalpunktzahlen aller
  Fragen. Um nicht versehentlich Daten zu verlieren, werden 
  bereits bestehende Dateien nicht überschrieben, sondern die 
  Daten angehängt.

Start des Servers
-----------------
  Startet man das Programm ohne Parameter, so wird die Datei
  "Beispielfragen.txt" geladen und alle Ergebnisse in den Dateien
  "Ergebnisse.txt" und "Ergebnisse.csv" gesichert.

  Möchte man beispielsweise die Datei "Test.txt" laden und die 
  Ergebnisse in den Dateien "Testergebnisse.txt" und 
  "Testergebnisse.csv" sichern, so hat man zwei Möglichkeiten, 
  dies auf der Konsole zu tun:

  Linux:
    ./mcv Test Testergebnisse
    ./mcv -in=Test -out=Testergebnisse

  Windows:
    ./mcv.exe Test Testergebnisse
    ./mcv.exe -in=Test -out=Testergebnisse

  Ohne die Flags "in" und "out" wird der erste Parameter immer als
  Fragendatei interpretiert.

  Gültige Zeichen für Dateinamen und Pfade sind:
  a-z A-Z 0-9 - _ / \ . ä Ä ö Ö ü Ü ß ( ) Space
  Alle anderen Zeichen werden entfernt. Damit Klammern oder
  Leerzeichen in Dateinamen verwendet werden können, müssen 
  Anführungszeichen verwendet werden. Beispiel:

  Linux:
    ./mcv -in="Test Klasse 9a (neu)" -out="Testergebnisse Klasse 9a (neu)"

  Windows:
    ./mcv.exe -in="Test Klasse 9a (neu)" -out="Testergebnisse Klasse 9a (neu)"

Test im Browser aufrufen
------------------------
  Wurde das Programm gestartet, zeigt es auf der Konsole die
  IP-Adresse an, welche die Schüler dann im Browser aufrufen
  müssen. Testet man das Programm auf demselben Rechner, kann
  statt der IP auch "localhost:8080" im Browser eingegeben werden.

Server beenden
--------------
  Der Server beendet sich nach zwei Stunden von allein, kann aber
  jederzeit mit Strg+c beendet werden.