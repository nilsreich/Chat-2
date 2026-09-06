package main

import (
	"crypto/subtle"
	"net/http"
	"time"
)

func (a *App) loginPage(w http.ResponseWriter, r *http.Request) {
	p := Page{Title: "Anmelden"}
	if a.prod && a.adminPassword == "" {
		p.Error = "Kein Administrator-Passwort konfiguriert."
	}
	render(w, p, loginHTML)
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	key := r.RemoteAddr
	if !a.loginAllowed(key, time.Now()) {
		http.Error(w, "Zu viele Anmeldeversuche. Bitte kurz warten.", http.StatusTooManyRequests)
		return
	}
	ok, configured := a.validPassword(r.FormValue("password"))
	if !configured {
		render(w, Page{Title: "Anmelden", Error: "Kein Administrator-Passwort konfiguriert."}, loginHTML)
		return
	}
	if !ok {
		a.recordLoginFailure(key, time.Now())
		render(w, Page{Title: "Anmelden", Error: "Passwort ist nicht korrekt."}, loginHTML)
		return
	}
	a.sessions.Put(r.Context(), "auth", true)
	a.clearLoginFailures(key)
	http.Redirect(w, r, "/", 303)
}

func (a *App) validPassword(submitted string) (valid, configured bool) {
	expected := a.adminPassword
	if !a.prod {
		expected = env("DEV_PASSWORD", "noten")
	}
	if expected == "" {
		return false, false
	}
	return subtle.ConstantTimeCompare([]byte(submitted), []byte(expected)) == 1, true
}

func (a *App) loginAllowed(key string, now time.Time) bool {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	attempt := a.loginAttempts[key]
	if now.Sub(attempt.Since) >= time.Minute {
		delete(a.loginAttempts, key)
		return true
	}
	return attempt.Count < 5
}

func (a *App) recordLoginFailure(key string, now time.Time) {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	if a.loginAttempts == nil {
		a.loginAttempts = make(map[string]loginAttempt)
	}
	attempt := a.loginAttempts[key]
	if attempt.Since.IsZero() || now.Sub(attempt.Since) >= time.Minute {
		attempt = loginAttempt{Since: now}
	}
	attempt.Count++
	a.loginAttempts[key] = attempt
}

func (a *App) clearLoginFailures(key string) {
	a.loginMu.Lock()
	delete(a.loginAttempts, key)
	a.loginMu.Unlock()
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	_ = a.sessions.Destroy(r.Context())
	http.Redirect(w, r, "/login", 303)
}
