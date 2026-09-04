ALTER TABLE classes ADD COLUMN deleted_at TEXT;
ALTER TABLE students ADD COLUMN deleted_at TEXT;
ALTER TABLE assessments ADD COLUMN deleted_at TEXT;

CREATE INDEX classes_deleted_idx ON classes(deleted_at);
CREATE INDEX students_class_deleted_idx ON students(class_id, deleted_at);
CREATE INDEX assessments_class_deleted_idx ON assessments(class_id, deleted_at);

CREATE TABLE audit_log (
    id INTEGER PRIMARY KEY,
    entity_type TEXT NOT NULL CHECK(entity_type IN ('class','student','assessment','grade')),
    entity_id INTEGER NOT NULL,
    assessment_id INTEGER,
    student_id INTEGER,
    action TEXT NOT NULL CHECK(action IN ('created','updated','deleted','restored','undo')),
    before_json TEXT NOT NULL,
    after_json TEXT NOT NULL,
    undone_at TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX audit_entity_idx ON audit_log(entity_type, entity_id, id DESC);
CREATE INDEX audit_grade_idx ON audit_log(assessment_id, student_id, id DESC);
