package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/example/noten/internal/calc"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql static/* static/icons/* sw.js
var files embed.FS

type sqliteStore struct{ db *sql.DB }

func (s sqliteStore) Delete(token string) error {
	_, e := s.db.Exec("DELETE FROM sessions WHERE token=?", token)
	return e
}
func (s sqliteStore) Find(token string) ([]byte, bool, error) {
	var b []byte
	e := s.db.QueryRow("SELECT data FROM sessions WHERE token=? AND expiry>?", token, time.Now().Unix()).Scan(&b)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, false, nil
	}
	return b, e == nil, e
}
func (s sqliteStore) Commit(token string, b []byte, expiry time.Time) error {
	_, e := s.db.Exec("INSERT INTO sessions(token,data,expiry) VALUES(?,?,?) ON CONFLICT(token) DO UPDATE SET data=excluded.data,expiry=excluded.expiry", token, b, expiry.Unix())
	return e
}
func (s sqliteStore) DeleteExpired() error {
	_, e := s.db.Exec("DELETE FROM sessions WHERE expiry<?", time.Now().Unix())
	return e
}

type App struct {
	db           *sql.DB
	sessions     *scs.SessionManager
	passwordHash string
	prod         bool
}
type Class struct {
	ID            int64
	Name, Subject string
	Written, Oral float64
}
type Student struct {
	ID, ClassID int64
	First, Last string
}
type Assessment struct {
	ID, ClassID      int64
	Name, Type, Date string
	Weight           float64
}
type Cell struct {
	Points *int
	Status string
}
type Row struct {
	Student Student
	Cells   map[int64]Cell
	Result  calc.Result
}
type Page struct {
	Title       string
	Classes     []Class
	Class       *Class
	Students    []Student
	Assessments []Assessment
	Rows        []Row
	Assessment  *Assessment
	Error       string
	Auth        bool
	Notice      string
	UndoID      int64
	History     []Audit
	Deleted     Trash
}

type Audit struct {
	ID, EntityID                                 int64
	EntityType, Action, Before, After, CreatedAt string
	AssessmentID, StudentID                      sql.NullInt64
}
type Trash struct {
	Classes     []Class
	Students    []Student
	Assessments []Assessment
}
type gradeState struct {
	Exists bool   `json:"exists"`
	Points *int   `json:"points"`
	Status string `json:"status"`
}
type classState struct {
	Name      string  `json:"name"`
	Subject   string  `json:"subject"`
	Written   float64 `json:"written_weight"`
	Oral      float64 `json:"oral_weight"`
	DeletedAt *string `json:"deleted_at"`
}
type studentState struct {
	First     string  `json:"first_name"`
	Last      string  `json:"last_name"`
	DeletedAt *string `json:"deleted_at"`
}
type assessmentState struct {
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	Date      string  `json:"date"`
	Weight    float64 `json:"weight"`
	DeletedAt *string `json:"deleted_at"`
}

func main() {
	dbpath := env("DATABASE_PATH", "./noten.db")
	if err := os.MkdirAll(filepath.Dir(dbpath), 0700); err != nil {
		panic(err)
	}
	db, err := sql.Open("sqlite", dbpath)
	if err != nil {
		panic(err)
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000", "PRAGMA synchronous=NORMAL"} {
		if _, err = db.Exec(q); err != nil {
			panic(err)
		}
	}
	if err = migrate(db); err != nil {
		panic(err)
	}
	sm := scs.New()
	sm.Store = sqliteStore{db}
	sm.Lifetime = 30 * 24 * time.Hour
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
	prod := os.Getenv("APP_ENV") == "production"
	sm.Cookie.Secure = prod
	a := &App{db: db, sessions: sm, passwordHash: os.Getenv("ADMIN_PASSWORD_HASH"), prod: prod}
	mux := http.NewServeMux()
	a.routes(mux)
	h := sm.LoadAndSave(a.security(mux))
	srv := &http.Server{Addr: ":" + env("PORT", "8080"), Handler: h, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		slog.Info("listening", "addr", srv.Addr)
		if e := srv.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
			panic(e)
		}
	}()
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	_ = db.Close()
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func migrate(db *sql.DB) error {
	_, e := db.Exec("CREATE TABLE IF NOT EXISTS schema_migrations(version TEXT PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)")
	if e != nil {
		return e
	}
	entries, _ := fs.ReadDir(files, "migrations")
	for _, f := range entries {
		var n int
		if e = db.QueryRow("SELECT count(*) FROM schema_migrations WHERE version=?", f.Name()).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			continue
		}
		b, _ := files.ReadFile("migrations/" + f.Name())
		tx, e := db.Begin()
		if e != nil {
			return e
		}
		if _, e = tx.Exec(string(b)); e == nil {
			_, e = tx.Exec("INSERT INTO schema_migrations(version) VALUES(?)", f.Name())
		}
		if e != nil {
			tx.Rollback()
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
	}
	return nil
}
func (a *App) routes(m *http.ServeMux) {
	static, _ := fs.Sub(files, "static")
	m.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	m.HandleFunc("GET /sw.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Service-Worker-Allowed", "/")
		http.ServeFileFS(w, r, files, "sw.js")
	})
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if a.db.Ping() != nil {
			http.Error(w, "unhealthy", 503)
			return
		}
		fmt.Fprint(w, "ok")
	})
	m.HandleFunc("GET /login", a.loginPage)
	m.HandleFunc("POST /login", a.login)
	m.HandleFunc("POST /logout", a.need(a.logout))
	m.HandleFunc("GET /", a.need(a.home))
	m.HandleFunc("POST /classes", a.need(a.createClass))
	m.HandleFunc("POST /classes/{id}/edit", a.need(a.editClass))
	m.HandleFunc("POST /classes/{id}/delete", a.need(a.deleteClass))
	m.HandleFunc("POST /classes/{id}/students", a.need(a.addStudents))
	m.HandleFunc("POST /students/{id}/edit", a.need(a.editStudent))
	m.HandleFunc("POST /students/{id}/delete", a.need(a.deleteStudent))
	m.HandleFunc("GET /students/{id}", a.need(a.studentDetail))
	m.HandleFunc("POST /classes/{id}/assessments", a.need(a.createAssessment))
	m.HandleFunc("POST /assessments/{id}/edit", a.need(a.editAssessment))
	m.HandleFunc("POST /assessments/{id}/delete", a.need(a.deleteAssessment))
	m.HandleFunc("GET /assessments/{id}/grades", a.need(a.gradeEntry))
	m.HandleFunc("POST /assessments/{id}/grades/{student}", a.need(a.saveGrade))
	m.HandleFunc("GET /classes/{id}/export.csv", a.need(a.exportCSV))
	m.HandleFunc("POST /changes/{id}/undo", a.need(a.undoChange))
	m.HandleFunc("POST /history/{id}/restore", a.need(a.restoreHistory))
	m.HandleFunc("GET /history", a.need(a.historyPage))
	m.HandleFunc("GET /trash", a.need(a.trashPage))
	m.HandleFunc("POST /trash/{type}/{id}/restore", a.need(a.restoreDeleted))
}
func (a *App) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			o := r.Header.Get("Origin")
			if o != "" && !strings.HasSuffix(o, "://"+r.Host) {
				http.Error(w, "Ungültige Anfrage", 403)
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/static/") {
			w.Header().Set("Cache-Control", "public, max-age=86400")
		} else {
			w.Header().Set("Cache-Control", "no-store")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}
func (a *App) need(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.sessions.GetBool(r.Context(), "auth") {
			http.Redirect(w, r, "/login", 303)
			return
		}
		h(w, r)
	}
}
func (a *App) loginPage(w http.ResponseWriter, r *http.Request) {
	render(w, Page{Title: "Anmelden"}, loginHTML)
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	ok := false
	if a.passwordHash != "" {
		ok = bcrypt.CompareHashAndPassword([]byte(a.passwordHash), []byte(r.FormValue("password"))) == nil
	} else if !a.prod {
		ok = r.FormValue("password") == env("DEV_PASSWORD", "noten")
	}
	if !ok {
		render(w, Page{Title: "Anmelden", Error: "Passwort ist nicht korrekt."}, loginHTML)
		return
	}
	a.sessions.Put(r.Context(), "auth", true)
	http.Redirect(w, r, "/", 303)
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	_ = a.sessions.Destroy(r.Context())
	http.Redirect(w, r, "/login", 303)
}
func id(r *http.Request, key string) int64 {
	v, _ := strconv.ParseInt(r.PathValue(key), 10, 64)
	return v
}
func (a *App) classes() []Class {
	rows, _ := a.db.Query("SELECT id,name,subject,written_weight,oral_weight FROM classes WHERE deleted_at IS NULL ORDER BY name")
	if rows == nil {
		return nil
	}
	defer rows.Close()
	var out []Class
	for rows.Next() {
		var x Class
		_ = rows.Scan(&x.ID, &x.Name, &x.Subject, &x.Written, &x.Oral)
		out = append(out, x)
	}
	return out
}
func (a *App) home(w http.ResponseWriter, r *http.Request) {
	cs := a.classes()
	cid, _ := strconv.ParseInt(r.URL.Query().Get("class"), 10, 64)
	if cid == 0 && len(cs) > 0 {
		cid = cs[0].ID
	}
	undo, _ := strconv.ParseInt(r.URL.Query().Get("undo"), 10, 64)
	p := Page{Title: "Noten", Classes: cs, Auth: true, UndoID: undo, Notice: r.URL.Query().Get("message")}
	if cid > 0 {
		p.Class = a.loadClass(cid)
		p.Students = a.students(cid)
		p.Assessments = a.assessments(cid)
		p.Rows = a.rows(cid, p.Students, p.Assessments)
	}
	render(w, p, appHTML)
}
func (a *App) loadClass(n int64) *Class {
	var c Class
	if a.db.QueryRow("SELECT id,name,subject,written_weight,oral_weight FROM classes WHERE id=? AND deleted_at IS NULL", n).Scan(&c.ID, &c.Name, &c.Subject, &c.Written, &c.Oral) != nil {
		return nil
	}
	return &c
}
func (a *App) students(cid int64) []Student {
	rs, _ := a.db.Query("SELECT id,class_id,first_name,last_name FROM students WHERE class_id=? AND deleted_at IS NULL ORDER BY sort_order,last_name,first_name", cid)
	if rs == nil {
		return nil
	}
	defer rs.Close()
	var v []Student
	for rs.Next() {
		var x Student
		rs.Scan(&x.ID, &x.ClassID, &x.First, &x.Last)
		v = append(v, x)
	}
	return v
}
func (a *App) assessments(cid int64) []Assessment {
	rs, _ := a.db.Query("SELECT id,class_id,name,type,date,weight FROM assessments WHERE class_id=? AND deleted_at IS NULL ORDER BY date,id", cid)
	if rs == nil {
		return nil
	}
	defer rs.Close()
	var v []Assessment
	for rs.Next() {
		var x Assessment
		rs.Scan(&x.ID, &x.ClassID, &x.Name, &x.Type, &x.Date, &x.Weight)
		v = append(v, x)
	}
	return v
}
func (a *App) rows(cid int64, ss []Student, as []Assessment) []Row {
	out := make([]Row, 0, len(ss))
	for _, s := range ss {
		row := Row{Student: s, Cells: map[int64]Cell{}}
		var items []calc.Item
		for _, x := range as {
			var pts sql.NullInt64
			var status string
			e := a.db.QueryRow("SELECT points,status FROM grades WHERE assessment_id=? AND student_id=?", x.ID, s.ID).Scan(&pts, &status)
			var pp *int
			if e == nil && pts.Valid {
				z := int(pts.Int64)
				pp = &z
			}
			row.Cells[x.ID] = Cell{pp, status}
			items = append(items, calc.Item{Points: pp, Type: x.Type, Weight: x.Weight, Absent: status == "absent"})
		}
		c := a.loadClass(cid)
		row.Result = calc.Calculate(items, c.Written, c.Oral)
		out = append(out, row)
	}
	return out
}
func back(w http.ResponseWriter, r *http.Request, cid int64) {
	http.Redirect(w, r, "/?class="+strconv.FormatInt(cid, 10), 303)
}
func changedBack(w http.ResponseWriter, r *http.Request, cid, auditID int64, message string) {
	http.Redirect(w, r, fmt.Sprintf("/?class=%d&undo=%d&message=%s", cid, auditID, url.QueryEscape(message)), http.StatusSeeOther)
}
func jsonText(v any) string { b, _ := json.Marshal(v); return string(b) }
func addAudit(tx *sql.Tx, typ string, entity int64, assessment, student any, action, before, after string) (int64, error) {
	res, err := tx.Exec("INSERT INTO audit_log(entity_type,entity_id,assessment_id,student_id,action,before_json,after_json) VALUES(?,?,?,?,?,?,?)", typ, entity, assessment, student, action, before, after)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
func nullableString(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	return &n.String
}
func classSnapshot(q interface{ QueryRow(string, ...any) *sql.Row }, id int64) (classState, error) {
	var s classState
	var d sql.NullString
	e := q.QueryRow("SELECT name,subject,written_weight,oral_weight,deleted_at FROM classes WHERE id=?", id).Scan(&s.Name, &s.Subject, &s.Written, &s.Oral, &d)
	s.DeletedAt = nullableString(d)
	return s, e
}
func studentSnapshot(q interface{ QueryRow(string, ...any) *sql.Row }, id int64) (studentState, error) {
	var s studentState
	var d sql.NullString
	e := q.QueryRow("SELECT first_name,last_name,deleted_at FROM students WHERE id=?", id).Scan(&s.First, &s.Last, &d)
	s.DeletedAt = nullableString(d)
	return s, e
}
func assessmentSnapshot(q interface{ QueryRow(string, ...any) *sql.Row }, id int64) (assessmentState, error) {
	var s assessmentState
	var d sql.NullString
	e := q.QueryRow("SELECT name,type,date,weight,deleted_at FROM assessments WHERE id=?", id).Scan(&s.Name, &s.Type, &s.Date, &s.Weight, &d)
	s.DeletedAt = nullableString(d)
	return s, e
}
func gradeSnapshot(q interface{ QueryRow(string, ...any) *sql.Row }, aid, sid int64) (gradeState, error) {
	var s gradeState
	var p sql.NullInt64
	e := q.QueryRow("SELECT points,status FROM grades WHERE assessment_id=? AND student_id=?", aid, sid).Scan(&p, &s.Status)
	if errors.Is(e, sql.ErrNoRows) {
		return gradeState{}, nil
	}
	if e != nil {
		return s, e
	}
	s.Exists = true
	if p.Valid {
		x := int(p.Int64)
		s.Points = &x
	}
	return s, nil
}
func (a *App) createClass(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	tx, e := a.db.Begin()
	if e != nil {
		http.Error(w, "Speichern fehlgeschlagen", 500)
		return
	}
	res, e := tx.Exec("INSERT INTO classes(name,subject) VALUES(?,?)", strings.TrimSpace(r.FormValue("name")), strings.TrimSpace(r.FormValue("subject")))
	if e != nil {
		tx.Rollback()
		http.Error(w, "Klasse konnte nicht erstellt werden", 400)
		return
	}
	n, _ := res.LastInsertId()
	after, _ := classSnapshot(tx, n)
	audit, e := addAudit(tx, "class", n, nil, nil, "created", "{}", jsonText(after))
	if e != nil || tx.Commit() != nil {
		http.Error(w, "Speichern fehlgeschlagen", 500)
		return
	}
	changedBack(w, r, n, audit, "Klasse erstellt")
}
func (a *App) editClass(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	n := id(r, "id")
	ww, _ := strconv.ParseFloat(r.FormValue("written"), 64)
	ow, _ := strconv.ParseFloat(r.FormValue("oral"), 64)
	if ww+ow != 100 {
		http.Error(w, "Gewichtungen müssen 100 ergeben", 400)
		return
	}
	tx, e := a.db.Begin()
	if e != nil {
		http.Error(w, "Speichern fehlgeschlagen", 500)
		return
	}
	before, e := classSnapshot(tx, n)
	if e != nil {
		tx.Rollback()
		http.NotFound(w, r)
		return
	}
	_, e = tx.Exec("UPDATE classes SET name=?,subject=?,written_weight=?,oral_weight=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND deleted_at IS NULL", r.FormValue("name"), r.FormValue("subject"), ww, ow, n)
	if e != nil {
		http.Error(w, "Ungültige Eingabe", 400)
		return
	}
	after, _ := classSnapshot(tx, n)
	audit, e := addAudit(tx, "class", n, nil, nil, "updated", jsonText(before), jsonText(after))
	if e != nil || tx.Commit() != nil {
		http.Error(w, "Speichern fehlgeschlagen", 500)
		return
	}
	changedBack(w, r, n, audit, "Klasse geändert")
}
func (a *App) deleteClass(w http.ResponseWriter, r *http.Request) {
	n := id(r, "id")
	tx, _ := a.db.Begin()
	before, e := classSnapshot(tx, n)
	if e != nil {
		tx.Rollback()
		http.NotFound(w, r)
		return
	}
	_, e = tx.Exec("UPDATE classes SET deleted_at=CURRENT_TIMESTAMP,updated_at=CURRENT_TIMESTAMP WHERE id=? AND deleted_at IS NULL", n)
	after, _ := classSnapshot(tx, n)
	audit, ae := addAudit(tx, "class", n, nil, nil, "deleted", jsonText(before), jsonText(after))
	if e != nil || ae != nil || tx.Commit() != nil {
		http.Error(w, "Löschen fehlgeschlagen", 500)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/?undo=%d&message=%s", audit, url.QueryEscape("Klasse gelöscht – wiederherstellbar")), 303)
}
func splitName(s string) (string, string) {
	p := strings.Fields(s)
	if len(p) == 0 {
		return "", ""
	}
	if len(p) == 1 {
		return p[0], ""
	}
	return strings.Join(p[:len(p)-1], " "), p[len(p)-1]
}
func (a *App) addStudents(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	cid := id(r, "id")
	tx, _ := a.db.Begin()
	for i, line := range strings.Split(r.FormValue("names"), "\n") {
		f, l := splitName(line)
		if f != "" {
			res, e := tx.Exec("INSERT INTO students(class_id,first_name,last_name,sort_order) VALUES(?,?,?,?)", cid, f, l, i)
			if e != nil {
				tx.Rollback()
				http.Error(w, "Speichern fehlgeschlagen", 500)
				return
			}
			sid, _ := res.LastInsertId()
			after, _ := studentSnapshot(tx, sid)
			if _, e = addAudit(tx, "student", sid, nil, nil, "created", "{}", jsonText(after)); e != nil {
				tx.Rollback()
				http.Error(w, "Speichern fehlgeschlagen", 500)
				return
			}
		}
	}
	tx.Commit()
	back(w, r, cid)
}
func (a *App) editStudent(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	sid := id(r, "id")
	var cid int64
	a.db.QueryRow("SELECT class_id FROM students WHERE id=?", sid).Scan(&cid)
	tx, _ := a.db.Begin()
	before, e := studentSnapshot(tx, sid)
	if e != nil {
		tx.Rollback()
		http.NotFound(w, r)
		return
	}
	_, e = tx.Exec("UPDATE students SET first_name=?,last_name=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND deleted_at IS NULL", r.FormValue("first"), r.FormValue("last"), sid)
	after, _ := studentSnapshot(tx, sid)
	audit, ae := addAudit(tx, "student", sid, nil, nil, "updated", jsonText(before), jsonText(after))
	if e != nil || ae != nil || tx.Commit() != nil {
		http.Error(w, "Speichern fehlgeschlagen", 500)
		return
	}
	changedBack(w, r, cid, audit, "Schüler geändert")
}
func (a *App) deleteStudent(w http.ResponseWriter, r *http.Request) {
	sid := id(r, "id")
	var cid int64
	a.db.QueryRow("SELECT class_id FROM students WHERE id=?", sid).Scan(&cid)
	tx, _ := a.db.Begin()
	before, e := studentSnapshot(tx, sid)
	if e != nil {
		tx.Rollback()
		http.NotFound(w, r)
		return
	}
	_, e = tx.Exec("UPDATE students SET deleted_at=CURRENT_TIMESTAMP,updated_at=CURRENT_TIMESTAMP WHERE id=? AND deleted_at IS NULL", sid)
	after, _ := studentSnapshot(tx, sid)
	audit, ae := addAudit(tx, "student", sid, nil, nil, "deleted", jsonText(before), jsonText(after))
	if e != nil || ae != nil || tx.Commit() != nil {
		http.Error(w, "Löschen fehlgeschlagen", 500)
		return
	}
	changedBack(w, r, cid, audit, "Schüler gelöscht – wiederherstellbar")
}
func (a *App) createAssessment(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	cid := id(r, "id")
	wt, _ := strconv.ParseFloat(r.FormValue("weight"), 64)
	tx, e := a.db.Begin()
	if e != nil {
		http.Error(w, "Speichern fehlgeschlagen", 500)
		return
	}
	res, e := tx.Exec("INSERT INTO assessments(class_id,name,type,date,weight) VALUES(?,?,?,?,?)", cid, r.FormValue("name"), r.FormValue("type"), r.FormValue("date"), wt)
	if e != nil {
		tx.Rollback()
		http.Error(w, "Ungültige Leistung", 400)
		return
	}
	n, _ := res.LastInsertId()
	after, _ := assessmentSnapshot(tx, n)
	_, e = addAudit(tx, "assessment", n, nil, nil, "created", "{}", jsonText(after))
	if e != nil || tx.Commit() != nil {
		http.Error(w, "Speichern fehlgeschlagen", 500)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/assessments/%d/grades", n), 303)
}
func (a *App) editAssessment(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	n := id(r, "id")
	wt, _ := strconv.ParseFloat(r.FormValue("weight"), 64)
	var cid int64
	a.db.QueryRow("SELECT class_id FROM assessments WHERE id=?", n).Scan(&cid)
	tx, e := a.db.Begin()
	if e != nil {
		http.Error(w, "Speichern fehlgeschlagen", 500)
		return
	}
	before, e := assessmentSnapshot(tx, n)
	if e != nil {
		tx.Rollback()
		http.NotFound(w, r)
		return
	}
	_, e = tx.Exec("UPDATE assessments SET name=?,type=?,date=?,weight=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND deleted_at IS NULL", r.FormValue("name"), r.FormValue("type"), r.FormValue("date"), wt, n)
	if e != nil {
		http.Error(w, "Ungültige Leistung", 400)
		return
	}
	after, _ := assessmentSnapshot(tx, n)
	audit, ae := addAudit(tx, "assessment", n, nil, nil, "updated", jsonText(before), jsonText(after))
	if e != nil || ae != nil || tx.Commit() != nil {
		http.Error(w, "Speichern fehlgeschlagen", 500)
		return
	}
	changedBack(w, r, cid, audit, "Leistung geändert")
}
func (a *App) deleteAssessment(w http.ResponseWriter, r *http.Request) {
	n := id(r, "id")
	var cid int64
	a.db.QueryRow("SELECT class_id FROM assessments WHERE id=?", n).Scan(&cid)
	tx, _ := a.db.Begin()
	before, e := assessmentSnapshot(tx, n)
	if e != nil {
		tx.Rollback()
		http.NotFound(w, r)
		return
	}
	_, e = tx.Exec("UPDATE assessments SET deleted_at=CURRENT_TIMESTAMP,updated_at=CURRENT_TIMESTAMP WHERE id=? AND deleted_at IS NULL", n)
	after, _ := assessmentSnapshot(tx, n)
	audit, ae := addAudit(tx, "assessment", n, nil, nil, "deleted", jsonText(before), jsonText(after))
	if e != nil || ae != nil || tx.Commit() != nil {
		http.Error(w, "Löschen fehlgeschlagen", 500)
		return
	}
	changedBack(w, r, cid, audit, "Leistung gelöscht – wiederherstellbar")
}
func (a *App) gradeEntry(w http.ResponseWriter, r *http.Request) {
	n := id(r, "id")
	var x Assessment
	if a.db.QueryRow("SELECT id,class_id,name,type,date,weight FROM assessments WHERE id=? AND deleted_at IS NULL", n).Scan(&x.ID, &x.ClassID, &x.Name, &x.Type, &x.Date, &x.Weight) != nil {
		http.NotFound(w, r)
		return
	}
	ss := a.students(x.ClassID)
	render(w, Page{Title: x.Name, Class: a.loadClass(x.ClassID), Students: ss, Assessment: &x, Rows: a.rows(x.ClassID, ss, []Assessment{x}), Auth: true}, gradeHTML)
}
func (a *App) saveGrade(w http.ResponseWriter, r *http.Request) {
	aid, sid := id(r, "id"), id(r, "student")
	r.ParseForm()
	v := r.FormValue("points")
	tx, e := a.db.Begin()
	if e != nil {
		http.Error(w, "Speichern fehlgeschlagen", 500)
		return
	}
	before, e := gradeSnapshot(tx, aid, sid)
	if e != nil {
		tx.Rollback()
		http.Error(w, "Speichern fehlgeschlagen", 500)
		return
	}
	display := "–"
	if v == "clear" {
		_, e = tx.Exec("DELETE FROM grades WHERE assessment_id=? AND student_id=?", aid, sid)
	} else if v == "absent" {
		display = "fehlt"
		_, e = tx.Exec("INSERT INTO grades(assessment_id,student_id,points,status) VALUES(?,?,NULL,'absent') ON CONFLICT(assessment_id,student_id) DO UPDATE SET points=NULL,status='absent',updated_at=CURRENT_TIMESTAMP", aid, sid)
	} else {
		p, pe := strconv.Atoi(v)
		if pe != nil || p < 0 || p > 15 {
			tx.Rollback()
			http.Error(w, "Note muss zwischen 0 und 15 liegen", 400)
			return
		}
		display = strconv.Itoa(p)
		_, e = tx.Exec("INSERT INTO grades(assessment_id,student_id,points,status) VALUES(?,?,?,'grade') ON CONFLICT(assessment_id,student_id) DO UPDATE SET points=excluded.points,status='grade',updated_at=CURRENT_TIMESTAMP", aid, sid, p)
	}
	if e != nil {
		tx.Rollback()
		http.Error(w, "Speichern fehlgeschlagen", 500)
		return
	}
	after, e := gradeSnapshot(tx, aid, sid)
	if e != nil {
		tx.Rollback()
		http.Error(w, "Speichern fehlgeschlagen", 500)
		return
	}
	var gid int64
	if after.Exists {
		e = tx.QueryRow("SELECT id FROM grades WHERE assessment_id=? AND student_id=?", aid, sid).Scan(&gid)
	} else if before.Exists {
		e = tx.QueryRow("SELECT COALESCE(MAX(entity_id),0) FROM audit_log WHERE assessment_id=? AND student_id=?", aid, sid).Scan(&gid)
		if gid == 0 {
			gid = -(aid*1000000 + sid)
		}
	} else {
		gid = -(aid*1000000 + sid)
	}
	action := "updated"
	if !before.Exists {
		action = "created"
	}
	audit, ae := addAudit(tx, "grade", gid, aid, sid, action, jsonText(before), jsonText(after))
	if e != nil || ae != nil || tx.Commit() != nil {
		http.Error(w, "Speichern fehlgeschlagen", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"display": display, "audit_id": audit, "message": "Note gespeichert"})
}
func (a *App) auditByID(tx *sql.Tx, n int64) (Audit, error) {
	var x Audit
	e := tx.QueryRow("SELECT id,entity_type,entity_id,assessment_id,student_id,action,before_json,after_json,created_at FROM audit_log WHERE id=? AND undone_at IS NULL", n).Scan(&x.ID, &x.EntityType, &x.EntityID, &x.AssessmentID, &x.StudentID, &x.Action, &x.Before, &x.After, &x.CreatedAt)
	return x, e
}
func (a *App) currentJSON(tx *sql.Tx, x Audit) (string, error) {
	switch x.EntityType {
	case "class":
		s, e := classSnapshot(tx, x.EntityID)
		return jsonText(s), e
	case "student":
		s, e := studentSnapshot(tx, x.EntityID)
		return jsonText(s), e
	case "assessment":
		s, e := assessmentSnapshot(tx, x.EntityID)
		return jsonText(s), e
	case "grade":
		s, e := gradeSnapshot(tx, x.AssessmentID.Int64, x.StudentID.Int64)
		return jsonText(s), e
	}
	return "", errors.New("unknown entity")
}
func applySnapshot(tx *sql.Tx, x Audit, raw string) error {
	switch x.EntityType {
	case "class":
		if raw == "{}" {
			_, e := tx.Exec("UPDATE classes SET deleted_at=CURRENT_TIMESTAMP,updated_at=CURRENT_TIMESTAMP WHERE id=?", x.EntityID)
			return e
		}
		var s classState
		if e := json.Unmarshal([]byte(raw), &s); e != nil {
			return e
		}
		_, e := tx.Exec("UPDATE classes SET name=?,subject=?,written_weight=?,oral_weight=?,deleted_at=?,updated_at=CURRENT_TIMESTAMP WHERE id=?", s.Name, s.Subject, s.Written, s.Oral, s.DeletedAt, x.EntityID)
		return e
	case "student":
		if raw == "{}" {
			_, e := tx.Exec("UPDATE students SET deleted_at=CURRENT_TIMESTAMP,updated_at=CURRENT_TIMESTAMP WHERE id=?", x.EntityID)
			return e
		}
		var s studentState
		if e := json.Unmarshal([]byte(raw), &s); e != nil {
			return e
		}
		_, e := tx.Exec("UPDATE students SET first_name=?,last_name=?,deleted_at=?,updated_at=CURRENT_TIMESTAMP WHERE id=?", s.First, s.Last, s.DeletedAt, x.EntityID)
		return e
	case "assessment":
		if raw == "{}" {
			_, e := tx.Exec("UPDATE assessments SET deleted_at=CURRENT_TIMESTAMP,updated_at=CURRENT_TIMESTAMP WHERE id=?", x.EntityID)
			return e
		}
		var s assessmentState
		if e := json.Unmarshal([]byte(raw), &s); e != nil {
			return e
		}
		_, e := tx.Exec("UPDATE assessments SET name=?,type=?,date=?,weight=?,deleted_at=?,updated_at=CURRENT_TIMESTAMP WHERE id=?", s.Name, s.Type, s.Date, s.Weight, s.DeletedAt, x.EntityID)
		return e
	case "grade":
		var s gradeState
		if e := json.Unmarshal([]byte(raw), &s); e != nil {
			return e
		}
		if !s.Exists {
			_, e := tx.Exec("DELETE FROM grades WHERE assessment_id=? AND student_id=?", x.AssessmentID.Int64, x.StudentID.Int64)
			return e
		}
		_, e := tx.Exec("INSERT INTO grades(assessment_id,student_id,points,status) VALUES(?,?,?,?) ON CONFLICT(assessment_id,student_id) DO UPDATE SET points=excluded.points,status=excluded.status,updated_at=CURRENT_TIMESTAMP", x.AssessmentID.Int64, x.StudentID.Int64, s.Points, s.Status)
		return e
	}
	return errors.New("unknown entity")
}
func (a *App) undoChange(w http.ResponseWriter, r *http.Request) {
	n := id(r, "id")
	tx, e := a.db.Begin()
	if e != nil {
		http.Error(w, "Rückgängig fehlgeschlagen", 500)
		return
	}
	x, e := a.auditByID(tx, n)
	if e != nil {
		tx.Rollback()
		http.Error(w, "Änderung ist nicht mehr verfügbar", 409)
		return
	}
	current, e := a.currentJSON(tx, x)
	if e != nil || current != x.After {
		tx.Rollback()
		http.Error(w, "Diese Änderung kann nicht mehr direkt rückgängig gemacht werden, weil der Wert inzwischen erneut geändert wurde.", 409)
		return
	}
	if e = applySnapshot(tx, x, x.Before); e != nil {
		tx.Rollback()
		http.Error(w, "Rückgängig fehlgeschlagen", 500)
		return
	}
	after, _ := a.currentJSON(tx, x)
	_, e = addAudit(tx, x.EntityType, x.EntityID, nullArg(x.AssessmentID), nullArg(x.StudentID), "undo", current, after)
	if e == nil {
		_, e = tx.Exec("UPDATE audit_log SET undone_at=CURRENT_TIMESTAMP WHERE id=?", n)
	}
	if e != nil || tx.Commit() != nil {
		http.Error(w, "Rückgängig fehlgeschlagen", 500)
		return
	}
	if r.FormValue("ajax") == "1" {
		display := ""
		if x.EntityType == "grade" {
			var gs gradeState
			_ = json.Unmarshal([]byte(after), &gs)
			if gs.Status == "absent" {
				display = "fehlt"
			} else if gs.Points != nil {
				display = strconv.Itoa(*gs.Points)
			} else {
				display = "–"
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "display": display})
		return
	}
	http.Redirect(w, r, r.FormValue("return"), 303)
}
func nullArg(n sql.NullInt64) any {
	if n.Valid {
		return n.Int64
	}
	return nil
}
func (a *App) restoreHistory(w http.ResponseWriter, r *http.Request) {
	n := id(r, "id")
	tx, _ := a.db.Begin()
	x, e := a.auditByID(tx, n)
	if e != nil || x.EntityType != "grade" {
		tx.Rollback()
		http.Error(w, "Verlaufseintrag nicht gefunden", 404)
		return
	}
	current, e := a.currentJSON(tx, x)
	if e != nil {
		tx.Rollback()
		http.Error(w, "Wiederherstellen fehlgeschlagen", 500)
		return
	}
	if e = applySnapshot(tx, x, x.Before); e != nil {
		tx.Rollback()
		http.Error(w, "Wiederherstellen fehlgeschlagen", 500)
		return
	}
	after, _ := a.currentJSON(tx, x)
	if _, e = addAudit(tx, "grade", x.EntityID, x.AssessmentID.Int64, x.StudentID.Int64, "restored", current, after); e != nil || tx.Commit() != nil {
		http.Error(w, "Wiederherstellen fehlgeschlagen", 500)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/history?assessment=%d&student=%d", x.AssessmentID.Int64, x.StudentID.Int64), 303)
}
func (a *App) historyPage(w http.ResponseWriter, r *http.Request) {
	typ := r.URL.Query().Get("type")
	entity, _ := strconv.ParseInt(r.URL.Query().Get("entity"), 10, 64)
	aid, _ := strconv.ParseInt(r.URL.Query().Get("assessment"), 10, 64)
	sid, _ := strconv.ParseInt(r.URL.Query().Get("student"), 10, 64)
	var rs *sql.Rows
	var e error
	if aid > 0 && sid > 0 {
		rs, e = a.db.Query("SELECT id,entity_type,entity_id,assessment_id,student_id,action,before_json,after_json,created_at FROM audit_log WHERE entity_type='grade' AND assessment_id=? AND student_id=? ORDER BY id DESC LIMIT 100", aid, sid)
	} else {
		rs, e = a.db.Query("SELECT id,entity_type,entity_id,assessment_id,student_id,action,before_json,after_json,created_at FROM audit_log WHERE entity_type=? AND entity_id=? ORDER BY id DESC LIMIT 100", typ, entity)
	}
	if e != nil {
		http.Error(w, "Verlauf konnte nicht geladen werden", 500)
		return
	}
	defer rs.Close()
	var hs []Audit
	for rs.Next() {
		var x Audit
		rs.Scan(&x.ID, &x.EntityType, &x.EntityID, &x.AssessmentID, &x.StudentID, &x.Action, &x.Before, &x.After, &x.CreatedAt)
		hs = append(hs, x)
	}
	render(w, Page{Title: "Änderungsverlauf", Classes: a.classes(), History: hs, Auth: true}, historyHTML)
}
func (a *App) trashPage(w http.ResponseWriter, r *http.Request) {
	var t Trash
	rs, _ := a.db.Query("SELECT id,name,subject,written_weight,oral_weight FROM classes WHERE deleted_at IS NOT NULL ORDER BY deleted_at DESC")
	if rs != nil {
		for rs.Next() {
			var x Class
			rs.Scan(&x.ID, &x.Name, &x.Subject, &x.Written, &x.Oral)
			t.Classes = append(t.Classes, x)
		}
		rs.Close()
	}
	rs, _ = a.db.Query("SELECT id,class_id,first_name,last_name FROM students WHERE deleted_at IS NOT NULL ORDER BY deleted_at DESC")
	if rs != nil {
		for rs.Next() {
			var x Student
			rs.Scan(&x.ID, &x.ClassID, &x.First, &x.Last)
			t.Students = append(t.Students, x)
		}
		rs.Close()
	}
	rs, _ = a.db.Query("SELECT id,class_id,name,type,date,weight FROM assessments WHERE deleted_at IS NOT NULL ORDER BY deleted_at DESC")
	if rs != nil {
		for rs.Next() {
			var x Assessment
			rs.Scan(&x.ID, &x.ClassID, &x.Name, &x.Type, &x.Date, &x.Weight)
			t.Assessments = append(t.Assessments, x)
		}
		rs.Close()
	}
	render(w, Page{Title: "Gelöschte Elemente", Classes: a.classes(), Deleted: t, Auth: true}, trashHTML)
}
func (a *App) restoreDeleted(w http.ResponseWriter, r *http.Request) {
	typ, n := r.PathValue("type"), id(r, "id")
	if typ != "class" && typ != "student" && typ != "assessment" {
		http.NotFound(w, r)
		return
	}
	tx, _ := a.db.Begin()
	var before, after string
	var e error
	switch typ {
	case "class":
		s, x := classSnapshot(tx, n)
		e = x
		before = jsonText(s)
		if e == nil {
			_, e = tx.Exec("UPDATE classes SET deleted_at=NULL,updated_at=CURRENT_TIMESTAMP WHERE id=? AND deleted_at IS NOT NULL", n)
			s, _ = classSnapshot(tx, n)
			after = jsonText(s)
		}
	case "student":
		s, x := studentSnapshot(tx, n)
		e = x
		before = jsonText(s)
		if e == nil {
			_, e = tx.Exec("UPDATE students SET deleted_at=NULL,updated_at=CURRENT_TIMESTAMP WHERE id=? AND deleted_at IS NOT NULL", n)
			s, _ = studentSnapshot(tx, n)
			after = jsonText(s)
		}
	case "assessment":
		s, x := assessmentSnapshot(tx, n)
		e = x
		before = jsonText(s)
		if e == nil {
			_, e = tx.Exec("UPDATE assessments SET deleted_at=NULL,updated_at=CURRENT_TIMESTAMP WHERE id=? AND deleted_at IS NOT NULL", n)
			s, _ = assessmentSnapshot(tx, n)
			after = jsonText(s)
		}
	}
	if e == nil {
		_, e = addAudit(tx, typ, n, nil, nil, "restored", before, after)
	}
	if e != nil || tx.Commit() != nil {
		http.Error(w, "Wiederherstellen fehlgeschlagen", 500)
		return
	}
	http.Redirect(w, r, "/trash", 303)
}
func (a *App) studentDetail(w http.ResponseWriter, r *http.Request) {
	sid := id(r, "id")
	var s Student
	if a.db.QueryRow("SELECT id,class_id,first_name,last_name FROM students WHERE id=?", sid).Scan(&s.ID, &s.ClassID, &s.First, &s.Last) != nil {
		http.NotFound(w, r)
		return
	}
	as := a.assessments(s.ClassID)
	render(w, Page{Title: s.First + " " + s.Last, Class: a.loadClass(s.ClassID), Students: []Student{s}, Assessments: as, Rows: a.rows(s.ClassID, []Student{s}, as), Auth: true}, studentHTML)
}
func (a *App) exportCSV(w http.ResponseWriter, r *http.Request) {
	cid := id(r, "id")
	c := a.loadClass(cid)
	if c == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="noten.csv"`)
	cw := csv.NewWriter(w)
	defer cw.Flush()
	as := a.assessments(cid)
	head := []string{"Vorname", "Nachname"}
	for _, x := range as {
		head = append(head, x.Name)
	}
	head = append(head, "Schriftlich", "Mündlich", "Gesamt")
	cw.Write(head)
	for _, row := range a.rows(cid, a.students(cid), as) {
		v := []string{row.Student.First, row.Student.Last}
		for _, x := range as {
			v = append(v, cell(row.Cells[x.ID]))
		}
		v = append(v, calc.Format(row.Result.Written), calc.Format(row.Result.Oral), calc.Format(row.Result.Overall))
		cw.Write(v)
	}
}
func cell(c Cell) string {
	if c.Status == "absent" {
		return "fehlt"
	}
	if c.Points == nil {
		return ""
	}
	return strconv.Itoa(*c.Points)
}
func render(w http.ResponseWriter, p Page, body string) {
	f := template.FuncMap{"avg": calc.Format, "cell": cell, "today": func() string { return time.Now().Format("2006-01-02") }}
	t := template.Must(template.New("page").Funcs(f).Parse(layout + body))
	if e := t.ExecuteTemplate(w, "layout", p); e != nil {
		slog.Error("render failed", "error", e)
	}
}

const layout = `{{define "layout"}}<!doctype html><html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="theme-color" content="#f7f7f5"><title>{{.Title}} · Noten</title><link rel="manifest" href="/static/app.webmanifest"><link rel="icon" href="/static/icons/icon.svg"><link rel="stylesheet" href="/static/app.css"><script defer src="/static/htmx.min.js"></script><script defer src="/static/app.js"></script></head><body>{{if .Notice}}<aside class="undo-bar" data-undo-bar><span>{{.Notice}}</span>{{if .UndoID}}<form method="post" action="/changes/{{.UndoID}}/undo"><input type="hidden" name="return" value="/"><button>Rückgängig</button></form>{{end}}</aside>{{end}}{{template "body" .}}</body></html>{{end}}`
const loginHTML = `{{define "body"}}<main class="login"><form method="post" class="panel"><h1>Noten</h1><p class="muted">Privates Gradebook</p>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}<label>Passwort<input name="password" type="password" required autofocus autocomplete="current-password"></label><button>Anmelden</button></form></main>{{end}}`
const appHTML = `{{define "body"}}<div class="shell"><aside><header><strong>Klassen</strong><form method="post" action="/logout"><button class="quiet">Abmelden</button></form></header><nav>{{range .Classes}}<a href="/?class={{.ID}}" {{if $.Class}}{{if eq .ID $.Class.ID}}class="active"{{end}}{{end}}>{{.Name}} <small>{{.Subject}}</small></a>{{end}}</nav><a class="trash-link" href="/trash">Gelöschte Elemente</a><details><summary>+ Klasse</summary><form method="post" action="/classes"><label>Name<input name="name" required></label><label>Fach<input name="subject"></label><button>Erstellen</button></form></details></aside><main class="workspace">{{if .Class}}<header class="top"><div><h1>{{.Class.Name}}</h1><span class="muted">{{.Class.Subject}}</span></div><button data-open="assessment">+ Leistung</button></header><div class="matrix"><table><thead><tr><th>Name</th>{{range .Assessments}}<th><a href="/assessments/{{.ID}}/grades">{{.Name}}</a><small>{{.Type}}</small></th>{{end}}<th>S</th><th>M</th><th>Ø</th></tr></thead><tbody>{{$page:=.}}{{range .Rows}}{{$row:=.}}<tr><th><a href="/students/{{.Student.ID}}">{{.Student.First}} {{.Student.Last}}</a></th>{{range $a:=$page.Assessments}}<td><a href="/assessments/{{$a.ID}}/grades">{{cell (index $row.Cells $a.ID)}}</a></td>{{end}}<td>{{avg .Result.Written}}</td><td>{{avg .Result.Oral}}</td><td class="overall">{{avg .Result.Overall}}</td></tr>{{else}}<tr><td colspan="99" class="empty">Noch keine Schüler. Über „Verwalten“ hinzufügen.</td></tr>{{end}}</tbody></table></div><details class="manage"><summary>Verwalten & Export</summary><div class="manage-grid"><form method="post" action="/classes/{{.Class.ID}}/edit"><h2>Klasse <a class="history-link" href="/history?type=class&entity={{.Class.ID}}">Verlauf</a></h2><label>Name<input name="name" value="{{.Class.Name}}" required></label><label>Fach<input name="subject" value="{{.Class.Subject}}"></label><label>Schriftlich %<input type="number" name="written" value="{{.Class.Written}}" min="0" max="100"></label><label>Mündlich %<input type="number" name="oral" value="{{.Class.Oral}}" min="0" max="100"></label><button>Speichern</button></form><form method="post" action="/classes/{{.Class.ID}}/students"><h2>Schüler hinzufügen</h2><label>Ein Name pro Zeile<textarea name="names" rows="6" required></textarea></label><button>Hinzufügen</button></form><section><h2>Schüler</h2>{{range .Students}}<form class="inline" method="post" action="/students/{{.ID}}/edit"><a class="history-link" href="/history?type=student&entity={{.ID}}">Verlauf</a><input name="first" value="{{.First}}" aria-label="Vorname"><input name="last" value="{{.Last}}" aria-label="Nachname"><button>Speichern</button><button class="danger" formaction="/students/{{.ID}}/delete" data-confirm="Schüler ausblenden? Vorhandene Noten bleiben erhalten und können wiederhergestellt werden.">Löschen</button></form>{{end}}</section><section><h2>Leistungen</h2>{{range .Assessments}}<form class="inline" method="post" action="/assessments/{{.ID}}/edit"><a class="history-link" href="/history?type=assessment&entity={{.ID}}">Verlauf</a><input name="name" value="{{.Name}}"><select name="type"><option value="written" {{if eq .Type "written"}}selected{{end}}>Klassenarbeit</option><option value="test" {{if eq .Type "test"}}selected{{end}}>Test</option><option value="oral" {{if eq .Type "oral"}}selected{{end}}>Mündlich</option></select><input name="date" type="date" value="{{.Date}}"><input name="weight" type="number" min="0.1" step="0.1" value="{{.Weight}}"><button>Speichern</button><button class="danger" formaction="/assessments/{{.ID}}/delete" data-confirm="Leistung ausblenden? Vorhandene Noten bleiben erhalten und können wiederhergestellt werden.">Löschen</button></form>{{end}}</section><p><a class="button" href="/classes/{{.Class.ID}}/export.csv">CSV exportieren</a></p><form method="post" action="/classes/{{.Class.ID}}/delete"><button class="danger" data-confirm="Klasse ausblenden? Alle Daten bleiben erhalten und können wiederhergestellt werden.">Klasse löschen</button></form></div></details><dialog id="assessment"><form method="post" action="/classes/{{.Class.ID}}/assessments"><header><h2>Neue Leistung</h2><button type="button" data-close aria-label="Schließen">×</button></header><label>Name<input name="name" required autofocus></label><label>Typ<select name="type" data-type><option value="test">Test</option><option value="written">Klassenarbeit</option><option value="oral">Mündliche Note</option></select></label><label>Datum<input name="date" type="date" value="{{today}}" required></label><label>Gewichtung<input name="weight" type="number" min="0.1" step="0.1" value="1" required></label><footer><button type="button" class="secondary" data-close>Abbrechen</button><button>Erstellen</button></footer></form></dialog>{{else}}<div class="welcome"><h1>Erste Klasse anlegen</h1><p>Lege links eine Klasse an und beginne direkt mit der Notenerfassung.</p></div>{{end}}</main></div>{{end}}`
const gradeHTML = `{{define "body"}}<main class="entry"><header class="top"><div><a href="/?class={{.Class.ID}}">← Matrix</a><h1>{{.Assessment.Name}}</h1><p class="muted">{{.Class.Name}} · {{.Assessment.Date}} · Gewicht {{.Assessment.Weight}}</p></div></header><div class="entry-grid"><ol class="student-list">{{range $i,$s:=.Students}}<li data-student="{{$s.ID}}" {{if eq $i 0}}class="active"{{end}}><span>{{$s.First}} {{$s.Last}}</span><span><output id="grade-{{$s.ID}}">{{cell (index (index $.Rows $i).Cells $.Assessment.ID)}}</output><a class="history-link" href="/history?assessment={{$.Assessment.ID}}&student={{$s.ID}}">Verlauf</a></span></li>{{end}}</ol><section class="pad" aria-label="Notenfeld"><p id="entry-status">Note wählen – danach geht es automatisch weiter.</p><div><button data-grade="0">0</button><button data-grade="1">1</button><button data-grade="2">2</button><button data-grade="3">3</button><button data-grade="4">4</button><button data-grade="5">5</button><button data-grade="6">6</button><button data-grade="7">7</button><button data-grade="8">8</button><button data-grade="9">9</button><button data-grade="10">10</button><button data-grade="11">11</button><button data-grade="12">12</button><button data-grade="13">13</button><button data-grade="14">14</button><button data-grade="15">15</button></div><footer><button data-grade="absent" class="secondary">Fehlt</button><button data-grade="clear" class="secondary">Leeren</button></footer><form id="grade-form" hidden method="post" data-action="/assessments/{{.Assessment.ID}}/grades/"><input name="points"></form></section></div></main>{{end}}`
const studentHTML = `{{define "body"}}<main class="detail"><a href="/?class={{.Class.ID}}">← {{.Class.Name}}</a><h1>{{.Title}}</h1>{{$r:=index .Rows 0}}<div class="averages"><div><small>Gesamt</small><strong>{{avg $r.Result.Overall}}</strong></div><div><small>Schriftlich</small><strong>{{avg $r.Result.Written}}</strong></div><div><small>Mündlich</small><strong>{{avg $r.Result.Oral}}</strong></div></div><table><tbody>{{range .Assessments}}<tr><th>{{.Name}}<small>{{.Date}}</small></th><td>{{cell (index $r.Cells .ID)}}</td></tr>{{end}}</tbody></table></main>{{end}}`

const historyHTML = `{{define "body"}}<main class="detail"><a href="/">← Noten</a><h1>Änderungsverlauf</h1><table><thead><tr><th>Zeit</th><th>Aktion</th><th>Vorher</th><th>Nachher</th><th></th></tr></thead><tbody>{{range .History}}<tr><td>{{.CreatedAt}}</td><td>{{.Action}}</td><td><code>{{.Before}}</code></td><td><code>{{.After}}</code></td><td>{{if eq .EntityType "grade"}}<form method="post" action="/history/{{.ID}}/restore"><button class="secondary">Vorherigen Wert wiederherstellen</button></form>{{end}}</td></tr>{{else}}<tr><td colspan="5">Noch keine Änderungen.</td></tr>{{end}}</tbody></table></main>{{end}}`
const trashHTML = `{{define "body"}}<main class="detail"><a href="/">← Noten</a><h1>Gelöschte Elemente</h1><h2>Klassen</h2>{{range .Deleted.Classes}}<form class="restore-row" method="post" action="/trash/class/{{.ID}}/restore"><span>{{.Name}} <small>{{.Subject}}</small></span><button>Wiederherstellen</button></form>{{else}}<p class="muted">Keine gelöschten Klassen.</p>{{end}}<h2>Schüler</h2>{{range .Deleted.Students}}<form class="restore-row" method="post" action="/trash/student/{{.ID}}/restore"><span>{{.First}} {{.Last}}</span><button>Wiederherstellen</button></form>{{else}}<p class="muted">Keine gelöschten Schüler.</p>{{end}}<h2>Leistungen</h2>{{range .Deleted.Assessments}}<form class="restore-row" method="post" action="/trash/assessment/{{.ID}}/restore"><span>{{.Name}} <small>{{.Date}}</small></span><button>Wiederherstellen</button></form>{{else}}<p class="muted">Keine gelöschten Leistungen.</p>{{end}}</main>{{end}}`
