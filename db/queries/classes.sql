-- name: ListClasses :many
SELECT * FROM classes WHERE deleted_at IS NULL ORDER BY name;
-- name: GetClass :one
SELECT * FROM classes WHERE id=? AND deleted_at IS NULL;
-- name: ListStudents :many
SELECT * FROM students WHERE class_id=? AND deleted_at IS NULL ORDER BY sort_order,last_name,first_name;
-- name: ListAssessments :many
SELECT * FROM assessments WHERE class_id=? AND deleted_at IS NULL ORDER BY date,id;
-- name: UpsertGrade :exec
INSERT INTO grades(assessment_id,student_id,points,status) VALUES(?,?,?,'grade') ON CONFLICT(assessment_id,student_id) DO UPDATE SET points=excluded.points,status='grade',updated_at=CURRENT_TIMESTAMP;
