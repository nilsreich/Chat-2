-- name: ListAuditForEntity :many
SELECT * FROM audit_log WHERE entity_type = ? AND entity_id = ? ORDER BY id DESC LIMIT ?;

-- name: ListAuditForGrade :many
SELECT * FROM audit_log WHERE entity_type = 'grade' AND assessment_id = ? AND student_id = ? ORDER BY id DESC LIMIT ?;

-- name: GetAuditChange :one
SELECT * FROM audit_log WHERE id = ?;

-- name: ListDeletedClasses :many
SELECT * FROM classes WHERE deleted_at IS NOT NULL ORDER BY deleted_at DESC;

-- name: ListDeletedStudents :many
SELECT * FROM students WHERE deleted_at IS NOT NULL ORDER BY deleted_at DESC;

-- name: ListDeletedAssessments :many
SELECT * FROM assessments WHERE deleted_at IS NOT NULL ORDER BY deleted_at DESC;
