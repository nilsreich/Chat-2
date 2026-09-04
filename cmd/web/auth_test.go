package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
)

func loginRequest(t *testing.T, a *App, password string) *httptest.ResponseRecorder {
	t.Helper()
	if a.sessions == nil {
		a.sessions = scs.New()
		a.sessions.Store = sqliteStore{db: a.db}
	}
	form := url.Values{"password": {password}}
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.sessions.LoadAndSave(http.HandlerFunc(a.login)).ServeHTTP(w, r)
	return w
}

func TestProductionPassword(t *testing.T) {
	a := testApp(t)
	a.prod = true
	a.adminPassword = "sicheres-passwort"

	w := loginRequest(t, a, "sicheres-passwort")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("correct production password: status=%d location=%q", w.Code, w.Header().Get("Location"))
	}

	w = loginRequest(t, a, "falsch")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Passwort ist nicht korrekt") {
		t.Fatalf("wrong production password unexpectedly accepted: status=%d body=%q", w.Code, w.Body.String())
	}
}

func TestProductionMissingPasswordFailsSafely(t *testing.T) {
	a := testApp(t)
	a.prod = true
	a.adminPassword = ""

	w := loginRequest(t, a, "noten")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Kein Administrator-Passwort konfiguriert") {
		t.Fatalf("missing configuration response: status=%d body=%q", w.Code, w.Body.String())
	}

	r := httptest.NewRequest(http.MethodGet, "/login", nil)
	w = httptest.NewRecorder()
	a.loginPage(w, r)
	if !strings.Contains(w.Body.String(), "Kein Administrator-Passwort konfiguriert") {
		t.Fatalf("login page did not explain missing configuration: %q", w.Body.String())
	}
}

func TestDevelopmentPasswords(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("DEV_PASSWORD", "")
		a := testApp(t)
		valid, configured := a.validPassword("noten")
		if !valid || !configured {
			t.Fatal("default development password was rejected")
		}
	})

	t.Run("override", func(t *testing.T) {
		t.Setenv("DEV_PASSWORD", "lokal-geheim")
		a := testApp(t)
		if valid, _ := a.validPassword("noten"); valid {
			t.Fatal("default remained valid after DEV_PASSWORD override")
		}
		if valid, configured := a.validPassword("lokal-geheim"); !valid || !configured {
			t.Fatal("DEV_PASSWORD override was rejected")
		}
	})
}
