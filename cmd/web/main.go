package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/alexedwards/scs/v2"
	gradebook "github.com/example/noten"
	"github.com/example/noten/internal/calc"
	_ "modernc.org/sqlite"
)

var files = gradebook.Files

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
	db            *sql.DB
	sessions      *scs.SessionManager
	adminPassword string
	prod          bool
	loginMu       sync.Mutex
	loginAttempts map[string]loginAttempt
}
type loginAttempt struct {
	Count int
	Since time.Time
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
	Title         string
	Classes       []Class
	Class         *Class
	Students      []Student
	Assessments   []Assessment
	Rows          []Row
	Assessment    *Assessment
	Error         string
	Auth          bool
	Notice        string
	UndoID        int64
	History       []Audit
	Deleted       Trash
	ActiveStudent int64
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
	a := &App{db: db, sessions: sm, adminPassword: os.Getenv("ADMIN_PASSWORD"), prod: prod, loginAttempts: make(map[string]loginAttempt)}
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
	entries, _ := fs.ReadDir(files, "db/migrations")
	for _, f := range entries {
		var n int
		if e = db.QueryRow("SELECT count(*) FROM schema_migrations WHERE version=?", f.Name()).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			continue
		}
		b, _ := files.ReadFile("db/migrations/" + f.Name())
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
	if a.db.QueryRow("SELECT a.id,a.class_id,a.name,a.type,a.date,a.weight FROM assessments a JOIN classes c ON c.id=a.class_id AND c.deleted_at IS NULL WHERE a.id=? AND a.deleted_at IS NULL", n).Scan(&x.ID, &x.ClassID, &x.Name, &x.Type, &x.Date, &x.Weight) != nil {
		http.NotFound(w, r)
		return
	}
	ss := a.students(x.ClassID)
	active, _ := strconv.ParseInt(r.URL.Query().Get("student"), 10, 64)
	if active == 0 && len(ss) > 0 {
		active = ss[0].ID
	}
	found := false
	for _, student := range ss {
		if student.ID == active {
			found = true
			break
		}
	}
	if !found && len(ss) > 0 {
		active = ss[0].ID
	}
	render(w, Page{Title: x.Name, Class: a.loadClass(x.ClassID), Students: ss, Assessment: &x, Rows: a.rows(x.ClassID, ss, []Assessment{x}), ActiveStudent: active, Auth: true}, gradeHTML)
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
	var classID int64
	e = tx.QueryRow(`SELECT a.class_id
		FROM assessments a
		JOIN students s ON s.id=? AND s.class_id=a.class_id AND s.deleted_at IS NULL
		JOIN classes c ON c.id=a.class_id AND c.deleted_at IS NULL
		WHERE a.id=? AND a.deleted_at IS NULL`, sid, aid).Scan(&classID)
	if errors.Is(e, sql.ErrNoRows) {
		tx.Rollback()
		http.Error(w, "Schüler und Leistung gehören nicht zu derselben aktiven Klasse.", http.StatusUnprocessableEntity)
		return
	}
	if e != nil {
		tx.Rollback()
		http.Error(w, "Speichern fehlgeschlagen", http.StatusInternalServerError)
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
