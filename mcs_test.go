package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHandleSubmitProcessesSessionOnlyOnce(t *testing.T) {
	previousTestdaten := testdaten
	testdaten = &TestDaten{
		Ergebnisdatei: filepath.Join(t.TempDir(), "Ergebnisse"),
		AnzSchueler:   1,
		aktiveTests: []AktiveTests{{
			Schueler:   "Ada Beispiel",
			SessionID:  "test-session",
			TestBeginn: time.Now().Add(-time.Minute),
		}},
	}
	t.Cleanup(func() {
		testdaten = previousTestdaten
	})

	statuses := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			form := url.Values{
				"sessionID":    {"test-session"},
				"schuelerName": {"Ada Beispiel"},
			}
			request := httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()
			handleSubmit(response, request)
			statuses <- response.Code
		}()
	}

	successes := 0
	conflicts := 0
	for i := 0; i < 2; i++ {
		switch status := <-statuses; status {
		case http.StatusOK:
			successes++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("unexpected response status: %d", status)
		}
	}

	if successes != 1 || conflicts != 1 {
		t.Fatalf("expected one successful submission and one conflict, got %d success(es) and %d conflict(s)", successes, conflicts)
	}
	if testdaten.AnzSchueler != 0 || len(testdaten.aktiveTests) != 0 {
		t.Fatalf("expected no active tests after submission, got count=%d, active=%d", testdaten.AnzSchueler, len(testdaten.aktiveTests))
	}

	csv, err := os.ReadFile(testdaten.Ergebnisdatei + ".csv")
	if err != nil {
		t.Fatalf("read CSV results: %v", err)
	}
	if count := strings.Count(string(csv), "Ada Beispiel"); count != 1 {
		t.Fatalf("expected exactly one CSV result, got %d", count)
	}

	report, err := os.ReadFile(testdaten.Ergebnisdatei + ".txt")
	if err != nil {
		t.Fatalf("read text results: %v", err)
	}
	if count := strings.Count(string(report), "NAME:   Ada Beispiel"); count != 1 {
		t.Fatalf("expected exactly one text report, got %d", count)
	}
}
