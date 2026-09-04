package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func testApp(t *testing.T) *App {
	t.Helper()
	db, err := sql.Open("sqlite", t.TempDir()+"/test.db")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000"} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err = migrate(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &App{db: db}
}

func seed(t *testing.T, a *App) (int64, int64, int64) {
	t.Helper()
	cr, e := a.db.Exec("INSERT INTO classes(name,subject) VALUES('11a','Mathe')")
	if e != nil {
		t.Fatal(e)
	}
	cid, _ := cr.LastInsertId()
	sr, _ := a.db.Exec("INSERT INTO students(class_id,first_name,last_name) VALUES(?,?,?)", cid, "Anna", "Becker")
	sid, _ := sr.LastInsertId()
	ar, _ := a.db.Exec("INSERT INTO assessments(class_id,name,type,date,weight) VALUES(?,?,?,'2026-09-04',1)", cid, "Test", "test")
	aid, _ := ar.LastInsertId()
	return cid, sid, aid
}

func post(handler func(http.ResponseWriter, *http.Request), path string, values url.Values, params map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range params {
		r.SetPathValue(k, v)
	}
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

func save(t *testing.T, a *App, aid, sid int64, value string) int64 {
	t.Helper()
	w := post(a.saveGrade, "/", url.Values{"points": {value}}, map[string]string{"id": itoa(aid), "student": itoa(sid)})
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	var x struct {
		AuditID int64 `json:"audit_id"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &x); e != nil {
		t.Fatal(e)
	}
	return x.AuditID
}
func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestGradeAuditUndoConflictAndHistoricalRestore(t *testing.T) {
	a := testApp(t)
	_, sid, aid := seed(t, a)
	first := save(t, a, aid, sid, "11")
	second := save(t, a, aid, sid, "9")
	var before, after string
	if e := a.db.QueryRow("SELECT before_json,after_json FROM audit_log WHERE id=?", first).Scan(&before, &after); e != nil {
		t.Fatal(e)
	}
	if before != `{"exists":false,"points":null,"status":""}` || !strings.Contains(after, `"points":11`) {
		t.Fatalf("unexpected history %s -> %s", before, after)
	}
	w := post(a.undoChange, "/", url.Values{"ajax": {"1"}}, map[string]string{"id": itoa(second)})
	if w.Code != 200 {
		t.Fatalf("undo: %d %s", w.Code, w.Body.String())
	}
	var points int
	if e := a.db.QueryRow("SELECT points FROM grades WHERE assessment_id=? AND student_id=?", aid, sid).Scan(&points); e != nil || points != 11 {
		t.Fatalf("points=%d err=%v", points, e)
	}
	var undos int
	a.db.QueryRow("SELECT count(*) FROM audit_log WHERE action='undo'").Scan(&undos)
	if undos != 1 {
		t.Fatalf("undo audit count=%d", undos)
	}
	third := save(t, a, aid, sid, "9")
	_ = save(t, a, aid, sid, "7")
	w = post(a.undoChange, "/", url.Values{"ajax": {"1"}}, map[string]string{"id": itoa(first)})
	if w.Code != 409 {
		t.Fatalf("stale undo status=%d", w.Code)
	}
	w = post(a.restoreHistory, "/", nil, map[string]string{"id": itoa(third)})
	if w.Code != 303 {
		t.Fatalf("restore status=%d: %s", w.Code, w.Body.String())
	}
	a.db.QueryRow("SELECT points FROM grades WHERE assessment_id=? AND student_id=?", aid, sid).Scan(&points)
	if points != 11 {
		t.Fatalf("restored points=%d", points)
	}
	var restored int
	a.db.QueryRow("SELECT count(*) FROM audit_log WHERE action='restored'").Scan(&restored)
	if restored != 1 {
		t.Fatalf("restore audit count=%d", restored)
	}
}

func TestSoftDeleteKeepsGradesAndRestore(t *testing.T) {
	a := testApp(t)
	cid, sid, aid := seed(t, a)
	save(t, a, aid, sid, "13")
	w := post(a.deleteStudent, "/", nil, map[string]string{"id": itoa(sid)})
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	if len(a.students(cid)) != 0 {
		t.Fatal("deleted student remained active")
	}
	var grades int
	a.db.QueryRow("SELECT count(*) FROM grades").Scan(&grades)
	if grades != 1 {
		t.Fatal("grade was deleted")
	}
	w = post(a.restoreDeleted, "/", nil, map[string]string{"type": "student", "id": itoa(sid)})
	if w.Code != 303 || len(a.students(cid)) != 1 {
		t.Fatal("student was not restored")
	}
	w = post(a.deleteAssessment, "/", nil, map[string]string{"id": itoa(aid)})
	if len(a.assessments(cid)) != 0 {
		t.Fatal("assessment remained active")
	}
	a.db.QueryRow("SELECT count(*) FROM grades").Scan(&grades)
	if grades != 1 {
		t.Fatal("assessment grade was deleted")
	}
	post(a.restoreDeleted, "/", nil, map[string]string{"type": "assessment", "id": itoa(aid)})
	if len(a.assessments(cid)) != 1 {
		t.Fatal("assessment was not restored")
	}
	w = post(a.deleteClass, "/", nil, map[string]string{"id": itoa(cid)})
	if len(a.classes()) != 0 {
		t.Fatal("class remained active")
	}
	post(a.restoreDeleted, "/", nil, map[string]string{"type": "class", "id": itoa(cid)})
	if len(a.classes()) != 1 {
		t.Fatal("class was not restored")
	}
}

func TestWeightChangesAuditAndUndo(t *testing.T) {
	a := testApp(t)
	cid, _, aid := seed(t, a)
	w := post(a.editAssessment, "/", url.Values{"name": {"Test"}, "type": {"test"}, "date": {"2026-09-04"}, "weight": {"2"}}, map[string]string{"id": itoa(aid)})
	if w.Code != 303 {
		t.Fatalf("assessment edit %d", w.Code)
	}
	var change int64
	a.db.QueryRow("SELECT max(id) FROM audit_log").Scan(&change)
	post(a.undoChange, "/", url.Values{"return": {"/"}}, map[string]string{"id": itoa(change)})
	var weight float64
	a.db.QueryRow("SELECT weight FROM assessments WHERE id=?", aid).Scan(&weight)
	if weight != 1 {
		t.Fatalf("weight=%v", weight)
	}
	w = post(a.editClass, "/", url.Values{"name": {"11a"}, "subject": {"Mathe"}, "written": {"60"}, "oral": {"40"}}, map[string]string{"id": itoa(cid)})
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	a.db.QueryRow("SELECT max(id) FROM audit_log").Scan(&change)
	post(a.undoChange, "/", url.Values{"return": {"/"}}, map[string]string{"id": itoa(change)})
	var written, oral float64
	a.db.QueryRow("SELECT written_weight,oral_weight FROM classes WHERE id=?", cid).Scan(&written, &oral)
	if written != 50 || oral != 50 {
		t.Fatalf("weights=%v/%v", written, oral)
	}
}

func TestMigrationUpgradesExistingDatabase(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/existing.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	one, err := files.ReadFile("migrations/001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(one)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE schema_migrations(version TEXT PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP); INSERT INTO schema_migrations(version) VALUES('001_initial.sql'); INSERT INTO classes(name,subject) VALUES('Bestand','Mathe')"); err != nil {
		t.Fatal(err)
	}
	if err = migrate(db); err != nil {
		t.Fatal(err)
	}
	var name string
	var deleted sql.NullString
	if err = db.QueryRow("SELECT name,deleted_at FROM classes").Scan(&name, &deleted); err != nil {
		t.Fatal(err)
	}
	if name != "Bestand" || deleted.Valid {
		t.Fatalf("existing row changed: %q %#v", name, deleted)
	}
	var auditTable int
	if err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='audit_log'").Scan(&auditTable); err != nil || auditTable != 1 {
		t.Fatalf("audit table missing: %d %v", auditTable, err)
	}
}
