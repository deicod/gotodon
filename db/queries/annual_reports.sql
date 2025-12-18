-- Generated Annual Reports queries

-- name: GetAnnualReport :one
SELECT * FROM generated_annual_reports WHERE id = $1;

-- name: GetAnnualReportByAccountAndYear :one
SELECT * FROM generated_annual_reports 
WHERE account_id = $1 AND year = $2;

-- name: ListAnnualReportsByAccount :many
SELECT * FROM generated_annual_reports 
WHERE account_id = $1
ORDER BY year DESC;

-- name: CreateAnnualReport :one
INSERT INTO generated_annual_reports (
    account_id, year, data, schema_version
) VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: MarkAnnualReportViewed :one
UPDATE generated_annual_reports SET
    viewed_at = now(),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateAnnualReportData :one
UPDATE generated_annual_reports SET
    data = $2,
    schema_version = $3,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteAnnualReport :exec
DELETE FROM generated_annual_reports WHERE id = $1;

-- name: GetUnviewedAnnualReports :many
SELECT * FROM generated_annual_reports 
WHERE account_id = $1 AND viewed_at IS NULL
ORDER BY year DESC;

-- name: CountAnnualReportsByAccount :one
SELECT COUNT(*) FROM generated_annual_reports WHERE account_id = $1;
